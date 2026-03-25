package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"github.com/BrandonDHaskell/Portunus/server/internal/config"
	"github.com/BrandonDHaskell/Portunus/server/internal/db"
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

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
