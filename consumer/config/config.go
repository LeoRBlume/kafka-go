package config

import (
	"os"
	"strconv"
	"time"

	kafka "github.com/segmentio/kafka-go"
)

type Config struct {
	KafkaBroker      string
	KafkaTopic       string
	KafkaGroupID     string
	KafkaStartOffset int64

	// Windowed aggregation
	WindowSize       time.Duration
	GracePeriod      time.Duration
	OutputTopic      string
	SnapshotInterval time.Duration
	StateDir         string

	Port string
}

func NewDefaultConfig() *Config {
	return &Config{
		KafkaBroker:      getEnv("KAFKA_BROKER", "localhost:9092"),
		KafkaTopic:       getEnv("KAFKA_TOPIC", "demo-topic"),
		KafkaGroupID:     getEnv("KAFKA_GROUP_ID", "demo-consumer-group"),
		KafkaStartOffset: getEnvInt64("KAFKA_START_OFFSET", kafka.FirstOffset),

		WindowSize:       getEnvDuration("WINDOW_SIZE", 5*time.Minute),
		GracePeriod:      getEnvDuration("GRACE_PERIOD", 1*time.Minute),
		OutputTopic:      getEnv("OUTPUT_TOPIC", "windowed-results"),
		SnapshotInterval: getEnvDuration("SNAPSHOT_INTERVAL", 30*time.Second),
		StateDir:         getEnv("STATE_DIR", "./state"),

		Port: getEnv("SERVER_PORT", "8082"),
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

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultValue
}
