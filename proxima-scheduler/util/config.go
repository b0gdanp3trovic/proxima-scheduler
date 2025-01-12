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
	InfluxDBToken      string
	DbName             string
	DatabaseEnabled    bool
	PingInterval       time.Duration
	ScoringInterval    time.Duration
	IncludedNamespaces []string
	SchedulerName      string
	NodeIP             string
	ConsulURL          string
	AdmissionCrtPath   string
	AdmissionKeyPath   string
	ClusterName        string
	ConsulCertPath     string
}

func LoadConfig() *Config {
	influxDBAddress := getEnv("INFLUXDB_ADDRESS", "http://localhost:8086")
	dbName := getEnv("INFLUXDB_DB_NAME", "proxima_scheduler")
	databaseEnabled := getEnvAsBool("DATABASE_ENABLED", true)
	pingInterval := getEnvAsDuration("PING_INTERVAL", 10*time.Second)
	scoringInterval := getEnvAsDuration("SCORING_INTERVAL", 30*time.Second)
	includedNamespaces := parseIncludedNamespaces("INCLUDED_NAMESPACES", []string{"default"})
	schedulerName := getEnv("SCHEDULER_NAME", "proxima-scheduler")
	nodeIP := getEnv("NODE_IP", "")
	consulURL := getEnv("CONSUL_URL", "")
	admissionCrtPath := getEnv("ADMISSION_CRT_PATH", "")
	admissionKeyPath := getEnv("ADMISSION_KEY_PATH", "")
	clusterName := getEnv("CLUSTER_NAME", "")
	consulCertPath := getEnv("CONSUL_CERT_PATH", "")
	influxDBToken := getEnv("INFLUXDB_TOKEN", "")

	return &Config{
		InfluxDBAddress:    influxDBAddress,
		InfluxDBToken:      influxDBToken,
		DbName:             dbName,
		DatabaseEnabled:    databaseEnabled,
		PingInterval:       pingInterval,
		ScoringInterval:    scoringInterval,
		IncludedNamespaces: includedNamespaces,
		SchedulerName:      schedulerName,
		NodeIP:             nodeIP,
		ConsulURL:          consulURL,
		AdmissionCrtPath:   admissionCrtPath,
		AdmissionKeyPath:   admissionKeyPath,
		ClusterName:        clusterName,
		ConsulCertPath:     consulCertPath,
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
