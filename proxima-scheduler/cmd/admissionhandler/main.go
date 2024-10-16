package main

import (
	"github.com/b0gdanp3trovic/proxima-scheduler/admissionhandler"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
)

func main() {
	cfg := util.LoadConfig()

	admissionHandler := admissionhandler.NewAdmissionHandler(cfg.ConsulURL)

	// Start admissionHandler
	admissionHandler.Start()

	select {}
}
