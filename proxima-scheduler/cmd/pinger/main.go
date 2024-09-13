package main

import (
	"log"
	"os"

	"github.com/b0gdanp3trovic/proxima-scheduler/pinger"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	client "github.com/influxdata/influxdb1-client/v2"
)

func main() {
	cfg := util.LoadConfig()
	clientset, err := util.GetClientset()

	if err != nil {
		log.Fatalf("Failed to obtain clientset: %v", err)
		os.Exit(1)
	}

	// Initialize latency DB
	influxClient, err := client.NewHTTPClient(client.HTTPConfig{
		Addr: cfg.InfluxDBAddress,
	})

	if err != nil {
		log.Fatalf("Failed to initialize InfluxDB client: %v", err)
		os.Exit(1)
	}

	influxDb := pinger.NewInfluxDB(influxClient, cfg.DatabaseName)

	pinger, err := pinger.NewPinger(cfg.PingInterval, clientset, cfg.DatabaseEnabled, influxDb)

	// Start pinger
	pinger.Run()

	// Block the function from exiting
	select {}
}
