package main

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	"github.com/BrandonDHaskell/Portunus/server/internal/replay"

	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

func main() {
	cfg, err := config.FromEnv()
	logger := log.New(os.Stdout, "portunus-server ", log.LstdFlags|log.LUTC)
	if err != nil {
		logger.Fatalf("configuration error: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		logger.Fatalf("configuration error:\n%v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --- Open / migrate / (dev) seed SQLite
	dbConn, err := db.Open(ctx, db.Config{Path: cfg.DBPath})
	if err != nil {
		logger.Fatalf("db open/init error: %v", err)
	}
	defer dbConn.Close()

	if cfg.Env == config.EnvLocal {
		if err := db.SeedDev(ctx, dbConn, db.SeedDevOptions{KnownModules: cfg.KnownModules}); err != nil {
			logger.Fatalf("db seed error: %v", err)
		}
	}
	// "ci" and "prod" intentionally skip dev seeding — CI manages its own
	// fixtures and prod must never auto-seed.
	writer := db.NewWorker(dbConn)
	defer writer.Close()

	deviceStore := sqlitestore.NewDeviceStore(dbConn, writer)
	heartbeatStore := sqlitestore.NewHeartbeatStore(dbConn, writer)
	accessEventStore := sqlitestore.NewAccessEventStore(dbConn, writer)
	moduleAdminStore := sqlitestore.NewModuleAdminStore(dbConn, writer)
	adminUserStore := sqlitestore.NewAdminUserStore(dbConn, writer)
	sessionStore := sqlitestore.NewSessionStore(dbConn, writer)
	roleStore := sqlitestore.NewRoleStore(dbConn, writer)
	memberAccessStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	moduleAuthStore := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)
	auditStore := sqlitestore.NewAuditStore(dbConn, writer)

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

	accessSvc := service.NewAccessService(registry, service.AccessPolicy{
		AllowAll: cfg.AllowAll,
	}, accessEventStore)

	credentialHashSecret := []byte(cfg.CredentialHashSecret)
	if cfg.CredentialHashSecret == "" {
		if !cfg.AllowUnkeyedCredentialHash {
			logger.Fatalf("PORTUNUS_CREDENTIAL_HASH_SECRET is not set. " +
				"Starting without a key stores reversible credential hashes. " +
				"Set PORTUNUS_CREDENTIAL_HASH_SECRET or, for dev/test only, " +
				"set PORTUNUS_ALLOW_UNKEYED_CREDENTIAL_HASH=true to override.")
		}
		logger.Printf("WARNING: PORTUNUS_CREDENTIAL_HASH_SECRET not set — credential IDs hashed without a key (INSECURE, dev/test only)")
	}
	accessSvc.SetCredentialHashSecret(credentialHashSecret)
	accessSvc.SetLogger(logger)

	// Member access + module authorization services.
	memberAccessSvc := service.NewMemberAccessService(memberAccessStore)
	moduleAuthSvc := service.NewModuleAuthorizationService(moduleAuthStore, memberAccessStore, auditStore)

	// Enable member_access + module_authorizations path in the access service.
	accessSvc.SetMemberAccessStore(memberAccessStore)
	accessSvc.SetModuleAuthStore(moduleAuthStore)
	if err := accessSvc.Validate(); err != nil {
		logger.Fatalf("access service wiring error: %v", err)
	}

	// Expiry worker: transitions member records to 'expired' on a scheduled interval.
	expiryWorker := service.NewExpiryWorker(memberAccessStore, auditStore, service.ExpiryWorkerConfig{
		IntervalMinutes: cfg.ExpiryWorkerIntervalMinutes,
		PendingTTLDays:  cfg.PendingTTLDays,
	}, logger)
	expiryWorker.Start(ctx)
	defer expiryWorker.Stop()

	// Provisioning service: handles device-initiated provisioning from PROVISIONING_CONSOLE modules.
	provisionSvc := service.NewProvisionService(registry, memberAccessStore, accessEventStore, credentialHashSecret, auditStore)

	// Admin service for module and door management via REST API.
	adminSvc := service.NewAdminService(moduleAdminStore, credentialHashSecret)

	// Auth service: session-based admin authentication.
	authSvc := service.NewAuthService(adminUserStore, sessionStore, roleStore, logger)
	// Write the first-run admin password to a private file instead of the log
	// stream (F-9).  Skip for in-memory DBs used in dev/test.
	if cfg.DBPath != ":memory:" && !strings.Contains(cfg.DBPath, "mode=memory") {
		authSvc.SetBootstrapPasswordFile(filepath.Join(filepath.Dir(cfg.DBPath), "initial-admin-password.txt"))
	}
	if err := authSvc.Bootstrap(ctx); err != nil {
		logger.Fatalf("auth bootstrap error: %v", err)
	}

	// Admin user and role management services.
	adminUserSvc := service.NewAdminUserService(adminUserStore, roleStore, auditStore)
	roleSvc := service.NewRoleService(roleStore)

	// Session sweeper: cleans up expired session rows hourly (F-11).
	sessionSweeper := service.NewSessionSweeper(sessionStore, time.Hour, logger)
	sessionSweeper.Start(ctx)
	defer sessionSweeper.Stop()

	// Shared replay store: one namespace for HTTP and gRPC so a nonce cannot be
	// replayed across transports (F-3).  Only allocated when HMAC is enabled.
	var sharedReplayStore *replay.Store
	if cfg.HMACSecret != "" {
		sharedReplayStore = replay.NewStore(60 * time.Second)
	}

	// Acquire the serving certificate once. prod loads it from PEM files; ci
	// generates an ephemeral self-signed cert in-process. Both the HTTP and the
	// gRPC listener serve with this same certificate.
	var servingCert tls.Certificate
	tlsEnabled := false
	switch {
	case cfg.TLSCertFile != "" && cfg.TLSKeyFile != "":
		c, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			logger.Fatalf("TLS cert load error: %v", err)
		}
		servingCert, tlsEnabled = c, true
	case cfg.Env == config.EnvCI:
		c, err := config.EphemeralCert()
		if err != nil {
			logger.Fatalf("ci ephemeral cert error: %v", err)
		}
		servingCert, tlsEnabled = c, true
		logger.Printf("TLS: ephemeral self-signed cert (ci profile, in-memory)")
	}

	// HTTP
	srv := httpapi.NewServer(httpapi.Dependencies{
		Logger:               logger,
		Addr:                 cfg.HTTPAddr,
		HeartbeatService:     heartbeatSvc,
		AccessService:        accessSvc,
		ProvisionService:     provisionSvc,
		AdminService:         adminSvc,
		AuthService:          authSvc,
		AdminUserService:     adminUserSvc,
		RoleService:          roleSvc,
		MemberAccessService:  memberAccessSvc,
		ModuleAuthService:    moduleAuthSvc,
		AuditStore:           auditStore,
		HMACSecret:           cfg.HMACSecret,
		ReplayStore:          sharedReplayStore,
		CredentialHashSecret: credentialHashSecret,
		TLSEnabled:           tlsEnabled,
	})

	go func() {
		if tlsEnabled {
			logger.Printf("listening (TLS) on %s", cfg.HTTPAddr)
			if err := srv.StartTLSConfig(servingCert); err != nil {
				logger.Printf("server error: %v", err)
				stop()
			}
		} else {
			logger.Printf("listening (plain HTTP, not recommended for production) on %s", cfg.HTTPAddr)
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
			interceptors = append(interceptors, grpcapi.HMACInterceptor(cfg.HMACSecret, sharedReplayStore))
			logger.Printf("gRPC HMAC auth: ENABLED (replay window: 60s)")
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

		// TLS for gRPC: the same certificate the HTTP server serves.
		if tlsEnabled {
			opts = append(opts, grpc.Creds(credentials.NewTLS(&tls.Config{
				Certificates: []tls.Certificate{servingCert},
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
			ProvisionService: provisionSvc,
			HMACSecret:       cfg.HMACSecret,
		})
		pb.RegisterPortunusServiceServer(grpcServer, grpcHandler)
		if cfg.Env != config.EnvProd {
			reflection.Register(grpcServer)
			logger.Printf("gRPC reflection: ENABLED (non-prod)")
		} else {
			logger.Printf("gRPC reflection: DISABLED (prod)")
		}

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
