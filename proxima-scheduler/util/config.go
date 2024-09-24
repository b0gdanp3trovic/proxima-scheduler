package util

import (
	"fmt"
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
	SchedulerName      string
	NodeIP             string
}

func LoadConfig() (*Config, error) {
	influxDBAddress, err := getEnvOrFail("INFLUXDB_ADDRESS")
	if err != nil {
		return nil, err
	}

	databaseName, err := getEnvOrFail("INFLUXDB_DB_NAME")
	if err != nil {
		return nil, err
	}

	databaseEnabled := getEnvAsBool("DATABASE_ENABLED", true)

	pingInterval := getEnvAsDuration("PING_INTERVAL", 10*time.Second)

	includedNamespaces, err := parseIncludedNamespaces("INCLUDED_NAMESPACES", []string{"default"})
	if err != nil {
		return nil, err
	}

	schedulerName, err := getEnvOrFail("SCHEDULER_NAME")
	if err != nil {
		return nil, err
	}

	nodeIP, err := getEnvOrFail("NODE_IP")
	if err != nil {
		return nil, err
	}

	return &Config{
		InfluxDBAddress:    influxDBAddress,
		DatabaseName:       databaseName,
		DatabaseEnabled:    databaseEnabled,
		PingInterval:       pingInterval,
		IncludedNamespaces: includedNamespaces,
		SchedulerName:      schedulerName,
		NodeIP:             nodeIP, // Assign Node IP to the config
	}, nil
}

func getEnvOrFail(key string) (string, error) {
	value, exists := os.LookupEnv(key)
	if !exists || value == "" {
		return "", fmt.Errorf("required environment variable %s is not set or empty", key)
	}
	return value, nil
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

func parseIncludedNamespaces(name string, defaultVal []string) ([]string, error) {
	valStr, err := getEnvOrFail(name)
	if err != nil {
		return nil, err
	}
	if valStr == "" {
		return defaultVal, nil
	}
	return strings.Split(valStr, ","), nil
}

func HomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE")
}
