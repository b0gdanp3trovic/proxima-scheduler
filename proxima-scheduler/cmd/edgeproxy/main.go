package main

import (
	"log"
	"os"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/edgeproxy"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

func main() {
	// Load config
	cfg := util.LoadConfig()

	inClusterClientset, err := util.GetInClusterClientset()
	if err != nil {
		log.Fatalf("Failed to obtain clientset: %v", err)
		os.Exit(1)
	}

	kubeconfigs, err := util.LoadKubeconfigs(cfg.KubeConfigsPath)

	influxClient := influxdb2.NewClient(cfg.InfluxDBAddress, cfg.InfluxDBToken)
	influxDb := util.NewInfluxDB(influxClient, "proxima", "proxima")

	latencyWorker := edgeproxy.NewMetricsWorker(100, influxDb, cfg.NodeIP, 1*time.Minute)
	latencyWorker.Start()

	// TODO - change
	cacheDuration := 10 * time.Second

	edgeProxy, err := edgeproxy.NewEdgeProxy(
		cfg.ConsulURL,
		latencyWorker,
		influxDb,
		inClusterClientset,
		kubeconfigs,
		cacheDuration,
		cfg.NodeIP,
		cfg.KindNetworkIP,
	)

	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
		os.Exit(1)
	}

	edgeProxy.Run()

	// Block the function from exiting
	select {}
}
