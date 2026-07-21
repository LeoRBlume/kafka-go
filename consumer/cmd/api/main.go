package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/LeoRBlume/go-libs/logger"
	"github.com/seu-usuario/kafka-go/consumer/internal/app"
)

func main() {
	logger.Setup(logger.Config{
		ServiceName: "aggregator",
		Level:       logger.LevelInfo,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := app.Run(ctx); err != nil {
		logger.Error(ctx, "main", "aggregator exited with error", err)
		os.Exit(1)
	}
}
