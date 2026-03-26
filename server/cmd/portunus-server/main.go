package main

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/config"
	"github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/grpcapi"
	"github.com/BrandonDHaskell/Portunus/server/internal/httpapi"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"

	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

func main() {
	cfg := config.FromEnv()
	logger := log.New(os.Stdout, "portunus-server ", log.LstdFlags|log.LUTC)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --- Open / migrate / (dev) seed SQLite
	dbConn, err := db.Open(ctx, db.Config{Path: cfg.DBPath, Env: cfg.Env})
	if err != nil {
		logger.Fatalf("db open/init error: %v", err)
	}
	defer dbConn.Close()

	if cfg.Env == "dev" {
		if err := db.SeedDev(ctx, dbConn, db.SeedDevOptions{KnownModules: cfg.KnownModules}); err != nil {
			logger.Fatalf("db seed error: %v", err)
		}
	}
	writer := db.NewWorker(dbConn)
	defer writer.Close()

	deviceStore := sqlitestore.NewDeviceStore(dbConn, writer)
	heartbeatStore := sqlitestore.NewHeartbeatStore(dbConn, writer)
	accessEventStore := sqlitestore.NewAccessEventStore(dbConn, writer)
	cardStore := sqlitestore.NewCardStore(dbConn, writer)
	moduleAdminStore := sqlitestore.NewModuleAdminStore(dbConn, writer)

	// Stores (Memory for dev testing with no DB)
	// deviceStore := memory.NewDeviceStore(cfg.KnownModules)
	// heartbeatStore := memory.New()
	// accessEventStore := memory.NewAccessEventStore()

	// Services
	registry := service.NewDeviceRegistry(deviceStore)
	heartbeatSvc := service.NewHeartbeatService(heartbeatStore, registry)

	// Heartbeat pruner (background goroutine)
	pruner := service.NewHeartbeatPruner(heartbeatStore, service.PrunerConfig{
		RetentionDays: cfg.HeartbeatRetentionDays,
		IntervalHours: cfg.PruneIntervalHours,
	}, logger)
	pruner.Start(ctx)
	defer pruner.Stop()

	allowed := make(map[string]struct{}, len(cfg.AllowedCardIDs))
	for _, c := range cfg.AllowedCardIDs {
		allowed[c] = struct{}{}
	}
	accessSvc := service.NewAccessService(registry, service.AccessPolicy{
		AllowAll:       cfg.AllowAll,
		AllowedCardIDs: allowed,
	}, accessEventStore)

	// Enable DB-backed card lookups (replaces the legacy AllowedCardIDs
	// env-var map).  When cards exist in the DB, the access service
	// hashes the incoming card ID and checks the cards table.  The
	// AllowedCardIDs map still works as a fallback when the DB is empty
	// or cardStore is nil.
	accessSvc.SetCardStore(cardStore)

	// Admin service for module/card/door management via REST API.
	adminSvc := service.NewAdminService(moduleAdminStore, cardStore)

	// HTTP
	srv := httpapi.NewServer(httpapi.Dependencies{
		Logger:           logger,
		Addr:             cfg.HTTPAddr,
		HeartbeatService: heartbeatSvc,
		AccessService:    accessSvc,
		AdminService:     adminSvc,
		HMACSecret:       cfg.HMACSecret,
		AdminAPIKey:      cfg.AdminAPIKey,
	})

	go func() {
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			logger.Printf("listening (TLS) on %s", cfg.HTTPAddr)
			if err := srv.StartTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil {
				logger.Printf("server error: %v", err)
				stop()
			}
		} else {
			logger.Printf("listening (plain HTTP — not recommended for production) on %s", cfg.HTTPAddr)
			if err := srv.Start(); err != nil {
				logger.Printf("server error: %v", err)
				stop()
			}
		}
	}()

	// ── gRPC server (optional — for ESP32 modules with gRPC firmware) ────
	var grpcServer *grpc.Server
	if cfg.GRPCAddr != "" {
		// Build interceptor chain: logging → HMAC auth.
		interceptors := []grpc.UnaryServerInterceptor{
			grpcapi.LoggingInterceptor(logger),
		}
		if cfg.HMACSecret != "" {
			interceptors = append(interceptors, grpcapi.HMACInterceptor(cfg.HMACSecret))
			logger.Printf("gRPC HMAC auth: ENABLED")
		} else {
			logger.Printf("gRPC HMAC auth: DISABLED (set PORTUNUS_HMAC_SECRET to enable)")
		}

		opts := []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(interceptors...),
		}

		// TLS for gRPC — uses the same cert/key as the HTTP server.
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
			if err != nil {
				logger.Fatalf("gRPC TLS cert load error: %v", err)
			}
			opts = append(opts, grpc.Creds(credentials.NewTLS(&tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			})))
		} else {
			logger.Printf("gRPC: running WITHOUT TLS (not recommended for production)")
		}

		grpcServer = grpc.NewServer(opts...)

		grpcHandler := grpcapi.NewServer(grpcapi.Dependencies{
			Logger:           logger,
			HeartbeatService: heartbeatSvc,
			AccessService:    accessSvc,
		})
		pb.RegisterPortunusServiceServer(grpcServer, grpcHandler)
		reflection.Register(grpcServer)

		go func() {
			lis, err := net.Listen("tcp", cfg.GRPCAddr)
			if err != nil {
				logger.Printf("gRPC listen error: %v", err)
				stop()
				return
			}
			logger.Printf("gRPC listening on %s", cfg.GRPCAddr)
			if err := grpcServer.Serve(lis); err != nil {
				logger.Printf("gRPC server error: %v", err)
				stop()
			}
		}()
	} else {
		logger.Printf("gRPC: DISABLED (set PORTUNUS_GRPC_ADDR to enable, e.g. :50051)")
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)

	// Gracefully stop the gRPC server if it was started.
	if grpcServer != nil {
		grpcServer.GracefulStop()
	}
}
