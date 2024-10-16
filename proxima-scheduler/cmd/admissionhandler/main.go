package main

import (
	"github.com/b0gdanp3trovic/proxima-scheduler/admissionhandler"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
)

func main() {
	cfg := util.LoadConfig()

	admissionHandler := admissionhandler.NewAdmissionHandler(cfg.ConsulURL, cfg.AdmissionCrtPath, cfg.AdmissionKeyPath)

	// Start admissionHandler
	admissionHandler.Start()

	select {}
}
