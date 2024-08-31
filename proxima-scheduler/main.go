package main

import (
	"fmt"
	"log"
	"os"

	"github.com/b0gdanp3trovic/proxima-scheduler/pinger"
	"github.com/b0gdanp3trovic/proxima-scheduler/scheduler"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	client "github.com/influxdata/influxdb1-client/v2"
)

func main() {
	cfg := util.LoadConfig()
	scheduler, err := scheduler.NewScheduler(cfg.SchedulerName, cfg.IncludedNamespaces)

	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
		os.Exit(1)
	}

	fmt.Println("Scheduler successfully configured.")

	// Start the scheduler
	scheduler.Run()

	// Initialize pinger DB
	influxClient, err := client.NewHTTPClient(client.HTTPConfig{
		Addr: cfg.InfluxDBAddress,
	})

	if err != nil {
		log.Fatalf("Failed to initialize InfluxDB client: %v", err)
		os.Exit(1)
	}

	influxDb := pinger.NewInfluxDB(influxClient, cfg.DatabaseName)

	pinger, err := pinger.NewPinger(cfg.PingInterval, cfg.DatabaseEnabled, influxDb)

	// Start pinger
	pinger.Run()
}
