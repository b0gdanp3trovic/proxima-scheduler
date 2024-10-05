package main

import (
	"log"
	"os"

	"github.com/b0gdanp3trovic/proxima-scheduler/edgeproxy"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	client "github.com/influxdata/influxdb1-client/v2"
)

func main() {
	// Load config
	cfg := util.LoadConfig()

	influxClient, err := client.NewHTTPClient(client.HTTPConfig{
		Addr: cfg.InfluxDBAddress,
	})

	if err != nil {
		log.Fatalf("Failed to initialize InfluxDB client: %v", err)
		os.Exit(1)
	}

	influxDb := util.NewInfluxDB(influxClient, cfg.DatabaseName)

	latencyWorker := edgeproxy.NewLatencyWorker(100, influxDb, cfg.NodeIP)
	latencyWorker.Start()

	edgeProxy := edgeproxy.NewEdgeProxy(cfg.ConsulURL, latencyWorker)
	edgeProxy.Run()

	// Block the function from exiting
	select {}
}
