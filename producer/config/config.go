package config

import (
	"os"
	"strconv"
)

type Config struct {
	KafkaBroker string
	KafkaTopic  string
	WorkerCount int
	BatchSize   int
	Port        string
}

func NewDefaultConfig() *Config {
	return &Config{
		KafkaBroker: getEnv("KAFKA_BROKER", "localhost:9092"),
		KafkaTopic:  getEnv("KAFKA_TOPIC", "demo-topic"),
		WorkerCount: getEnvInt("WORKER_COUNT", 5000),
		BatchSize:   getEnvInt("BATCH_SIZE", 100),
		Port:        getEnv("SERVER_PORT", "8081"),
	}
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}
