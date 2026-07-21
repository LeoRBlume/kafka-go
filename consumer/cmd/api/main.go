package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/LeoRBlume/go-libs/logger"
	"github.com/seu-usuario/kafka-go/consumer/internal/app"
)

func main() {
	logger.Setup(logger.Config{
		ServiceName: "aggregator",
		Level:       logLevelFromEnv(),
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := app.Run(ctx); err != nil {
		logger.Error(ctx, "main", "aggregator exited with error", err)
		os.Exit(1)
	}
}

// logLevelFromEnv lê LOG_LEVEL (debug|info|warn|error); default info.
func logLevelFromEnv() logger.Level {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return logger.LevelDebug
	case "warn":
		return logger.LevelWarn
	case "error":
		return logger.LevelError
	default:
		return logger.LevelInfo
	}
}
