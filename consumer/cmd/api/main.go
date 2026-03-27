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
		ServiceName: "consumer",
		Level:       logger.LevelInfo,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := config.NewDefaultConfig()

	logger.Infof(ctx, "main", "starting consumer on port %s", cfg.Port)

	svc := service.NewConsumerService(cfg)
	defer svc.Close()

	go func() {
		if err := svc.Start(ctx); err != nil {
			logger.Error(ctx, "main", "consumer stopped with error", err)
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
			logger.Error(ctx, "main", "server error", err)
			os.Exit(1)
		}
	}()

	logger.Infof(ctx, "main", "consumer running on port %s", cfg.Port)
	<-ctx.Done()

	logger.Info(ctx, "main", "shutting down consumer")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error(shutdownCtx, "main", "shutdown error", err)
	}
}
