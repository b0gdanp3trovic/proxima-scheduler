package main

import (
	"fmt"
	"log"
	"os"

	"github.com/b0gdanp3trovic/proxima-scheduler/scheduler"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

func main() {
	cfg := util.LoadConfig()

	clientset, err := util.GetClientset()

	if err != nil {
		log.Fatalf("Failed to obtain clientset: %v", err)
		os.Exit(1)
	}

	influxClient := influxdb2.NewClient(cfg.InfluxDBAddress, cfg.InfluxDBToken)
	influxDb := util.NewInfluxDB(influxClient, "proxima", "proxima")

	//schedulerWorker, err := scheduler.NewScheduler(cfg.SchedulerName, cfg.IncludedNamespaces, clientset, influxDb)
	//
	//if err != nil {
	//	log.Fatalf("Failed to create scheduler: %v", err)
	//	os.Exit(1)
	//}
	//
	//fmt.Println("Scheduler successfully configured.")
	//
	//// Start the scheduler
	//schedulerWorker.Run()
	//fmt.Println("Run scheduler.")

	scoresWorker := scheduler.NewScoresWorker(clientset, influxDb, cfg.ScoringInterval, cfg.EdgeProxies)
	scoresWorker.Run()

	fmt.Println("Run scores worker.")

	// Block the function from exiting
	select {}
}
