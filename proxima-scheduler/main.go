package main

import (
	"fmt"
	"log"
	"os"

	"github.com/b0gdanp3trovic/proxima-scheduler/scheduler"
	"github.com/b0gdanp3trovic/proxima-scheduler/util"
)

func main() {
	cfg := util.LoadConfig()
	scheduler, err := scheduler.NewScheduler(cfg.IncludedNamespaces)

	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
		os.Exit(1)
	}

	fmt.Println("Scheduler successfully configured.")

	// Start the scheduler
	scheduler.Run()
}
