package main

import (
	"log"
	"os"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/noderegister"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
)

func main() {

	cfg := util.LoadConfig()

	clientset, err := util.GetClientset()

	if err != nil {
		log.Fatalf("Failed to obtain clientset: %v", err)
		os.Exit(1)
	}

	noderegister, err := noderegister.NewNodeRegister(10*time.Second, clientset, cfg.ConsulURL, cfg.ClusterName, cfg.ConsulCertPath)

	if err != nil {
		log.Fatalf("Failed to create node registrator: %v", err)
		os.Exit(1)
	}
	noderegister.Run()

	// Block the function from exiting
	select {}
}
