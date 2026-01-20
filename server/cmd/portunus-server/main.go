package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/config"
	"github.com/BrandonDHaskell/Portunus/server/internal/httpapi"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
)

func main() {
	cfg := config.FromEnv()
	logger := log.New(os.Stdout, "portunus-server ", log.LstdFlags|log.LUTC)

	// Stores (memory for now)
	deviceStore := memory.NewDeviceStore(cfg.KnownModules)
	heartbeatStore := memory.New() // your existing Heartbeat memory store (implements HeartbeatStore)

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
