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

	// Stores (Memory for dev testing with no DB)
	// deviceStore := memory.NewDeviceStore(cfg.KnownModules)
	// heartbeatStore := memory.New()

	// Services
	registry := service.NewDeviceRegistry(deviceStore)
	heartbeatSvc := service.NewHeartbeatService(heartbeatStore, registry)

	allowed := make(map[string]struct{}, len(cfg.AllowedCardIDs))
	for _, c := range cfg.AllowedCardIDs {
		allowed[c] = struct{}{}
	}
	accessSvc := service.NewAccessService(registry, service.AccessPolicy{
		AllowAll:       cfg.AllowAll,
		AllowedCardIDs: allowed,
	})

	// HTTP
	srv := httpapi.NewServer(httpapi.Dependencies{
		Logger:           logger,
		Addr:             cfg.HTTPAddr,
		HeartbeatService: heartbeatSvc,
		AccessService:    accessSvc,
	})

	go func() {
		logger.Printf("listening on %s", cfg.HTTPAddr)
		if err := srv.Start(); err != nil {
			logger.Printf("server error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
