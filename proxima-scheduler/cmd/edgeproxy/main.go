package main

import (
	"github.com/b0gdanp3trovic/proxima-scheduler/edgeproxy"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
)

func main() {
	cfg := util.LoadConfig()
	edgeProxy := edgeproxy.NewEdgeProxy(cfg.ConsulURL)

	edgeProxy.Run()
	// Block the function from exiting
	select {}
}
