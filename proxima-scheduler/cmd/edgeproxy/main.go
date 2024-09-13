package main

import (
	"github.com/b0gdanp3trovic/proxima-scheduler/edgeproxy"
)

func main() {
	edgeProxy := edgeproxy.NewEdgeProxy()

	edgeProxy.Run()
	// Block the function from exiting
	select {}
}
