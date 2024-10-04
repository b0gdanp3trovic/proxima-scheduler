package main

import (
	"github.com/b0gdanp3trovic/proxima-scheduler/edgeproxy"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
)

func main() {
	// Load config
	cfg := util.LoadConfig()

	// Create a LatencyWorker with a buffer size of 100
	latencyWorker := edgeproxy.NewLatencyWorker(100)

	// Start the LatencyWorker in a separate goroutine
	latencyWorker.Start()

	// Create and run EdgeProxy, passing the latency worker
	edgeProxy := edgeproxy.NewEdgeProxy(cfg.ConsulURL, latencyWorker)
	edgeProxy.Run()

	// Block the function from exiting
	select {}
}
