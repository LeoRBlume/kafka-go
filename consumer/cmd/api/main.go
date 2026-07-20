package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LeoRBlume/go-libs/logger"
	"github.com/seu-usuario/kafka-go/consumer/config"
	"github.com/seu-usuario/kafka-go/consumer/internal/controller"
	"github.com/seu-usuario/kafka-go/consumer/internal/router"
	"github.com/seu-usuario/kafka-go/consumer/internal/service"
)

func main() {
	logger.Setup(logger.Config{
		ServiceName: "aggregator",
		Level:       logger.LevelInfo,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := config.NewDefaultConfig()

	logger.Infof(ctx, "main", "starting aggregator on port %s (window=%s grace=%s output=%s)",
		cfg.Port, cfg.WindowSize, cfg.GracePeriod, cfg.OutputTopic)

	svc, err := service.NewAggregatorService(cfg)
	if err != nil {
		logger.Error(ctx, "main", "failed to create aggregator service", err)
		os.Exit(1)
	}
	defer svc.Close()

	go func() {
		if err := svc.Start(ctx); err != nil {
			logger.Error(ctx, "main", "aggregator stopped with error", err)
		}
	}()

	ctrl := controller.NewAggregatorController(svc)
	r := router.SetupRouter(ctrl)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error(ctx, "main", "server error", err)
			os.Exit(1)
		}
	}()

	logger.Infof(ctx, "main", "aggregator running on port %s", cfg.Port)
	<-ctx.Done()

	logger.Info(ctx, "main", "shutting down aggregator")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error(shutdownCtx, "main", "shutdown error", err)
	}
}
