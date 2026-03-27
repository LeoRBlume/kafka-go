package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LeoRBlume/go-libs/logger"
	"github.com/seu-usuario/kafka-go/producer/config"
	"github.com/seu-usuario/kafka-go/producer/internal/controller"
	"github.com/seu-usuario/kafka-go/producer/internal/router"
	"github.com/seu-usuario/kafka-go/producer/internal/service"
)

func main() {
	logger.Setup(logger.Config{
		ServiceName: "producer",
		Level:       logger.LevelInfo,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := config.NewDefaultConfig()

	logger.Infof(ctx, "main", "starting producer on port %s", cfg.Port)

	svc := service.NewProducerService(cfg)
	defer svc.Close()

	ctrl := controller.NewProduceController(svc)
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

	logger.Infof(ctx, "main", "producer running on port %s", cfg.Port)
	<-ctx.Done()

	logger.Info(ctx, "main", "shutting down producer")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error(shutdownCtx, "main", "shutdown error", err)
	}
}
