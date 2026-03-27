package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/seu-usuario/kafka-go/producer/config"
	"github.com/seu-usuario/kafka-go/producer/internal/controller"
	"github.com/seu-usuario/kafka-go/producer/internal/router"
	"github.com/seu-usuario/kafka-go/producer/internal/service"
)

func main() {
	cfg := config.NewDefaultConfig()

	slog.Info("starting producer", "port", cfg.Port)

	svc := service.NewProducerService(cfg)
	defer svc.Close()

	ctrl := controller.NewProduceController(svc)
	r := router.SetupRouter(ctrl)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("producer running", "port", cfg.Port)
	<-quit

	slog.Info("shutting down producer")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}
