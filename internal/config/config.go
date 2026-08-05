package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// Config holds all configuration for the application.
// Values are loaded from environment variables with sensible defaults.
type Config struct {
	Server  ServerConfig
	DB      DBConfig
	Redis   RedisConfig
	JWT     JWTConfig
	Gateway GatewayConfig
	Kafka   KafkaConfig
}

type ServerConfig struct {
	Host string
	Port string
	// Mode controls which components to run.
	// "all" (default): everything — HTTP, WebSocket, Kafka producer.
	// "gateway": WebSocket + message routing only.
	// "api": REST API only (auth, friends, groups).
	// "worker": Kafka consumer only (message persistence pipeline).
	Mode string
}

// GatewayConfig holds settings for a single gateway instance.
type GatewayConfig struct {
	// InstanceID uniquely identifies this gateway among peers.
	// Used to avoid delivering a message to the same instance twice
	// via pub/sub round-trip.
	InstanceID string
}

// KafkaConfig holds settings for the Kafka event bus.
// All fields are optional — if Brokers is empty, Kafka integration is disabled.
type KafkaConfig struct {
	// Brokers is a comma-separated list of bootstrap servers.
	Brokers string
	// TopicMessages is the topic for message events (private + group).
	TopicMessages string
	// ConsumerGroup is the group ID for Kafka consumers.
	ConsumerGroup string
}

type DBConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	SSLMode  string
}

// DSN returns the PostgreSQL connection string.
func (c DBConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.User, c.Password, c.Host, c.Port, c.Name, c.SSLMode,
	)
}

type RedisConfig struct {
	Host     string
	Port     string
	Password string
	DB       int
}

// Addr returns the Redis address in "host:port" format.
func (c RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%s", c.Host, c.Port)
}

type JWTConfig struct {
	AccessSecret  string
	RefreshSecret string
	AccessExpiry  time.Duration
	RefreshExpiry time.Duration
}

// Load reads configuration from environment variables.
// It returns an error if required values are missing.
func Load() (*Config, error) {
	var (
		dbHost          string
		dbUser          string
		dbPassword      string
		dbName          string
		accessSecret    string
		refreshSecret   string
		err             error
	)

	dbHost, err = requireEnv("DB_HOST")
	if err != nil {
		return nil, err
	}
	dbUser, err = requireEnv("DB_USER")
	if err != nil {
		return nil, err
	}
	dbPassword, err = requireEnv("DB_PASSWORD")
	if err != nil {
		return nil, err
	}
	dbName, err = requireEnv("DB_NAME")
	if err != nil {
		return nil, err
	}
	accessSecret, err = requireEnv("JWT_ACCESS_SECRET")
	if err != nil {
		return nil, err
	}
	refreshSecret, err = requireEnv("JWT_REFRESH_SECRET")
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Server: ServerConfig{
			Host: getEnv("SERVER_HOST", "0.0.0.0"),
			Port: getEnv("SERVER_PORT", "8080"),
			Mode: getEnv("SERVER_MODE", "all"),
		},
		DB: DBConfig{
			Host:     dbHost,
			Port:     getEnv("DB_PORT", "5432"),
			User:     dbUser,
			Password: dbPassword,
			Name:     dbName,
			SSLMode:  getEnv("DB_SSLMODE", "disable"),
		},
		Redis: RedisConfig{
			Host:     getEnv("REDIS_HOST", "localhost"),
			Port:     getEnv("REDIS_PORT", "6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       0,
		},
		JWT: JWTConfig{
			AccessSecret:  accessSecret,
			RefreshSecret: refreshSecret,
			AccessExpiry:  parseDuration(getEnv("JWT_ACCESS_EXPIRY", "15m"), 15*time.Minute),
			RefreshExpiry: parseDuration(getEnv("JWT_REFRESH_EXPIRY", "168h"), 168*time.Hour),
		},
		Gateway: GatewayConfig{
			// Random instance ID if not set — avoids accidental collisions
			// in development while still allowing explicit IDs in production.
			InstanceID: getEnv("GATEWAY_INSTANCE_ID", randomInstanceID()),
		},
		Kafka: KafkaConfig{
			Brokers:       getEnv("KAFKA_BROKERS", ""),
			TopicMessages: getEnv("KAFKA_TOPIC_MESSAGES", "im.messages"),
			ConsumerGroup: getEnv("KAFKA_CONSUMER_GROUP", "im-worker"),
		},
	}
	return cfg, nil
}

// getEnv returns the value of an environment variable or a default.
func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// requireEnv returns the value of a required environment variable.
// Returns an error if the value is empty so callers can handle it gracefully.
func requireEnv(key string) (string, error) {
	val := os.Getenv(key)
	if val == "" {
		return "", fmt.Errorf("required environment variable %s is not set", key)
	}
	return val, nil
}

// parseDuration parses a duration string with a fallback.
func parseDuration(s string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

// randomInstanceID generates a random 4-byte hex string for gateway instances.
// In production, set GATEWAY_INSTANCE_ID explicitly (e.g., via Kubernetes pod name).
// This default makes development zero-config while avoiding collisions.
func randomInstanceID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read can only fail on plan9/wasm; fall back to fixed.
		return "gw-0000"
	}
	return "gw-" + hex.EncodeToString(b)
}
