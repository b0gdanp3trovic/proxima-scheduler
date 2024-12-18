package main

import (
	"fmt"
	"log"
	"os"

	"github.com/b0gdanp3trovic/proxima-scheduler/scheduler"
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

	influxDb, err := util.NewInfluxDB(influxClient, cfg.DbName)
	if err != nil {
		log.Fatalf("Failed to initialize influx db: %v", err)
	}

	schedulerWorker, err := scheduler.NewScheduler(cfg.SchedulerName, cfg.IncludedNamespaces, clientset, influxDb)

	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
		os.Exit(1)
	}

	fmt.Println("Scheduler successfully configured.")

	// Start the scheduler
	schedulerWorker.Run()
	fmt.Println("Run scheduler.")

	scoresWorker := scheduler.NewScoresWorker(clientset, influxDb, cfg.ScoringInterval)
	scoresWorker.Run()

	fmt.Println("Run scores worker.")

	// Block the function from exiting
	select {}
}
