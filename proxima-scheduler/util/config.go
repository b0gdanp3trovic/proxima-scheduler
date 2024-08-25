package util

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	InfluxDBAddress    string
	DatabaseName       string
	DatabaseEnabled    bool
	PingInterval       time.Duration
	IncludedNamespaces []string
}

func LoadConfig() *Config {
	influxDBAddress := getEnv("INFLUXDB_ADDRESS", "http://localhost:8086")
	databaseName := getEnv("INFLUXDB_DB_NAME", "ping_db")
	databaseEnabled := getEnvAsBool("DATABASE_ENABLED", true)
	pingInterval := getEnvAsDuration("PING_INTERVAL", 10*time.Second)
	includedNamespaces := parseIncludedNamespaces("INCLUDED_NAMESPACES", []string{"default"})

	return &Config{
		InfluxDBAddress:    influxDBAddress,
		DatabaseName:       databaseName,
		DatabaseEnabled:    databaseEnabled,
		PingInterval:       pingInterval,
		IncludedNamespaces: includedNamespaces,
	}
}

func getEnv(key string, defaultValue string) string {
	value, exists := os.LookupEnv(key)
	if !exists {
		return defaultValue
	}
	return value
}

func getEnvAsBool(name string, defaultVal bool) bool {
	valStr := getEnv(name, "")
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.ParseBool(valStr)
	if err != nil {
		log.Printf("Invalid boolean value %s for %s, using default %t", valStr, name, defaultVal)
		return defaultVal
	}
	return val
}

func getEnvAsDuration(name string, defaultVal time.Duration) time.Duration {
	valStr := getEnv(name, "")
	if valStr == "" {
		return defaultVal
	}
	val, err := time.ParseDuration(valStr)
	if err != nil {
		log.Printf("Invalid duration value %s for %s, using default %s", valStr, name, defaultVal)
		return defaultVal
	}
	return val
}

func parseIncludedNamespaces(name string, defaultVal []string) []string {
	valStr := getEnv(name, "")
	if valStr == "" {
		return defaultVal
	}
	return strings.Split(valStr, ",")
}

func HomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE")
}
