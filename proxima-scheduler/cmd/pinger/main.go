package main

import (
	"log"
	"os"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

func main() {
	cfg := util.LoadConfig()

	_, err := util.GetClientset()

	if err != nil {
		log.Fatalf("Failed to obtain clientset: %v", err)
		os.Exit(1)
	}

	// Initialize latency DB
	influxdb2.NewClient(cfg.InfluxDBAddress, cfg.InfluxDBToken)

	if err != nil {
		log.Fatalf("Failed to initialize InfluxDB client: %v", err)
		os.Exit(1)
	}

	//influxDb, err := util.NewInfluxDB(influxClient, cfg.DbName)
	//if err != nil {
	//	log.Fatalf("Failed to initialize influx db: %v", err)
	//}
	//
	//pinger, err := pinger.NewPinger(cfg.PingInterval, clientset, cfg.DatabaseEnabled, influxDb, cfg.NodeIP)
	//
	//// Start pinger
	//pinger.Run()

	// Block the function from exiting
	select {}
}
