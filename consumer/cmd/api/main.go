package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/seu-usuario/kafka-go/consumer/config"
	"github.com/seu-usuario/kafka-go/consumer/internal/controller"
	"github.com/seu-usuario/kafka-go/consumer/internal/router"
	"github.com/seu-usuario/kafka-go/consumer/internal/service"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := config.NewDefaultConfig()

	slog.Info("starting consumer", "port", cfg.Port)

	svc := service.NewConsumerService(cfg)
	defer svc.Close()

	go func() {
		if err := svc.Start(ctx); err != nil {
			slog.Error("consumer stopped with error", "error", err)
		}
	}()

	ctrl := controller.NewHealthController()
	r := router.SetupRouter(ctrl)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("consumer running", "port", cfg.Port)
	<-ctx.Done()

	slog.Info("shutting down consumer")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}
