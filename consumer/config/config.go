package config

import (
	"os"
	"strconv"

	kafka "github.com/segmentio/kafka-go"
)

type Config struct {
	KafkaBroker      string
	KafkaTopic       string
	KafkaGroupID     string
	KafkaStartOffset int64
	Port             string
}

func NewDefaultConfig() *Config {
	return &Config{
		KafkaBroker:      getEnv("KAFKA_BROKER", "localhost:9092"),
		KafkaTopic:       getEnv("KAFKA_TOPIC", "demo-topic"),
		KafkaGroupID:     getEnv("KAFKA_GROUP_ID", "demo-consumer-group"),
		KafkaStartOffset: getEnvInt64("KAFKA_START_OFFSET", kafka.FirstOffset),
		Port:             getEnv("SERVER_PORT", "8082"),
	}
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return defaultValue
}
