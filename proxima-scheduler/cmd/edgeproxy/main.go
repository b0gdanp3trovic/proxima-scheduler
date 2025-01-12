package main

import (
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/edgeproxy"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

func main() {
	// Load config
	cfg := util.LoadConfig()

	influxClient := influxdb2.NewClient(cfg.InfluxDBAddress, cfg.InfluxDBToken)
	influxDb := util.NewInfluxDB(influxClient, "proxima", "proxima")

	latencyWorker := edgeproxy.NewMetricsWorker(100, influxDb, cfg.NodeIP, 1*time.Minute)
	latencyWorker.Start()

	// TODO - change
	cacheDuration := 10 * time.Second

	edgeProxy := edgeproxy.NewEdgeProxy(cfg.ConsulURL, latencyWorker, influxDb, cacheDuration, cfg.NodeIP)
	edgeProxy.Run()

	// Block the function from exiting
	select {}
}
