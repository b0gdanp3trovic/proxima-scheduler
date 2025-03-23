package main

import (
	"log"
	"os"

	"github.com/b0gdanp3trovic/proxima-scheduler/scheduler"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

func main() {
	cfg := util.LoadConfig()

	inClusterClientset, err := util.GetInClusterClientset()
	if err != nil {
		log.Fatalf("Failed to obtain clientset: %v", err)
		os.Exit(1)
	}

	kubeconfigs, err := util.LoadKubeconfigs(cfg.KubeConfigsPath)

	influxClient := influxdb2.NewClient(cfg.InfluxDBAddress, cfg.InfluxDBToken)
	influxDb := util.NewInfluxDB(influxClient, "proxima", "proxima")

	schedulerWorker, err := scheduler.NewScheduler(cfg.SchedulerName, cfg.IncludedNamespaces, cfg.EdgeProxies, inClusterClientset, kubeconfigs, influxDb)

	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
		os.Exit(1)
	}

	log.Println("Scheduler successfully configured.")

	// Start the scheduler
	schedulerWorker.Run()
	log.Println("Run scheduler.")

	scoresWorker := scheduler.NewScoresWorker(inClusterClientset, influxDb, cfg.ScoringInterval, cfg.EdgeProxies)
	scoresWorker.Run()

	log.Println("Run scores worker.")

	// Block the function from exiting
	select {}
}
