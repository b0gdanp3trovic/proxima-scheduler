package main

import (
	"log"
	"os"

	"github.com/b0gdanp3trovic/proxima-scheduler/pinger"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

func main() {
	cfg := util.LoadConfig()

	clientset, err := util.GetInClusterClientset()

	if err != nil {
		log.Fatalf("Failed to obtain clientset: %v", err)
		os.Exit(1)
	}

	// Initialize latency DB
	influxClient := influxdb2.NewClient(cfg.InfluxDBAddress, cfg.InfluxDBToken)
	influxDb := util.NewInfluxDB(influxClient, "proxima", "proxima")

	pinger, err := pinger.NewPinger(cfg.PingInterval, clientset, cfg.DatabaseEnabled, influxDb, cfg.NodeIP, cfg.EdgeProxies)
	if err != nil {
		log.Fatalf("Failed to initialize pinger: %v", err)
		os.Exit(1)
	}

	// Start pinger
	pinger.Run()

	// Block the function from exiting
	select {}
}
