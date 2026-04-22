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
	"google.golang.org/grpc/keepalive"
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

	if err := cfg.Validate(); err != nil {
		logger.Fatalf("configuration error:\n%v", err)
	}

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
	credentialStore := sqlitestore.NewCredentialStore(dbConn, writer)
	moduleAdminStore := sqlitestore.NewModuleAdminStore(dbConn, writer)
	adminUserStore := sqlitestore.NewAdminUserStore(dbConn, writer)
	sessionStore := sqlitestore.NewSessionStore(dbConn, writer)
	roleStore := sqlitestore.NewRoleStore(dbConn, writer)
	memberAccessStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	moduleAuthStore := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)

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

	allowed := make(map[string]struct{}, len(cfg.AllowedCredentialIDs))
	for _, c := range cfg.AllowedCredentialIDs {
		allowed[c] = struct{}{}
	}
	accessSvc := service.NewAccessService(registry, service.AccessPolicy{
		AllowAll:             cfg.AllowAll,
		AllowedCredentialIDs: allowed,
	}, accessEventStore)

	// Enable DB-backed credential lookups (replaces the legacy AllowedCredentialIDs
	// env-var map).  When credentials exist in the DB, the access service
	// hashes the incoming credential ID and checks the credentials table.
	accessSvc.SetCredentialStore(credentialStore)

	credentialHashSecret := []byte(cfg.CredentialHashSecret)
	if cfg.CredentialHashSecret == "" {
		logger.Printf("WARNING: PORTUNUS_CREDENTIAL_HASH_SECRET not set — credential IDs hashed without a key (insecure, dev only)")
	}
	accessSvc.SetCredentialHashSecret(credentialHashSecret)

	// Member access + module authorization services (PR 4).
	memberAccessSvc := service.NewMemberAccessService(memberAccessStore, roleStore)
	moduleAuthSvc := service.NewModuleAuthorizationService(moduleAuthStore)

	// Enable member_access + module_authorizations path in the access service.
	accessSvc.SetMemberAccessStore(memberAccessStore)
	accessSvc.SetModuleAuthStore(moduleAuthStore)

	// Expiry worker: transitions member records to 'expired' on a scheduled interval.
	expiryWorker := service.NewExpiryWorker(memberAccessStore, service.ExpiryWorkerConfig{
		IntervalMinutes: cfg.ExpiryWorkerIntervalMinutes,
	}, logger)
	expiryWorker.Start(ctx)
	defer expiryWorker.Stop()

	// Admin service for module/credential/door management via REST API.
	adminSvc := service.NewAdminService(moduleAdminStore, credentialStore, credentialHashSecret)

	// Auth service: session-based admin authentication.
	authSvc := service.NewAuthService(adminUserStore, sessionStore, roleStore, logger)
	if err := authSvc.Bootstrap(ctx); err != nil {
		logger.Fatalf("auth bootstrap error: %v", err)
	}

	tlsEnabled := cfg.TLSCertFile != "" && cfg.TLSKeyFile != ""

	// HTTP
	srv := httpapi.NewServer(httpapi.Dependencies{
		Logger:              logger,
		Addr:                cfg.HTTPAddr,
		HeartbeatService:    heartbeatSvc,
		AccessService:       accessSvc,
		AdminService:        adminSvc,
		AuthService:         authSvc,
		MemberAccessService: memberAccessSvc,
		ModuleAuthService:   moduleAuthSvc,
		HMACSecret:          cfg.HMACSecret,
		TLSEnabled:          tlsEnabled,
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
			grpc.KeepaliveParams(keepalive.ServerParameters{
				MaxConnectionIdle: 5 * time.Minute,
				Time:              2 * time.Minute,
				Timeout:           20 * time.Second,
			}),
			grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
				MinTime:             30 * time.Second,
				PermitWithoutStream: true,
			}),
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
				NextProtos:   []string{"h2"},
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
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			grpcServer.Stop()
		}
	}
}
