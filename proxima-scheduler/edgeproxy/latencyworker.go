package edgeproxy

import (
	"log"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
)

type LatencyData struct {
	ServiceName string
	PodURL      string
	NodeIP      string
	Latency     time.Duration
	Timestamp   time.Time
}

type LatencyWorker struct {
	latencyChan chan LatencyData
	database    util.Database
	hostNodeIP  string
}

func NewLatencyWorker(bufferSize int, db util.Database, hostNodeIP string) *LatencyWorker {
	latencyChan := make(chan LatencyData, bufferSize)
	return &LatencyWorker{
		latencyChan: latencyChan,
		database:    db,
		hostNodeIP:  hostNodeIP,
	}
}

func (lw *LatencyWorker) Start() {
	log.Printf("Starting latency worker...")
	go func() {
		for data := range lw.latencyChan {
			log.Printf("Processing latency data: %v for pod: %s", data.Latency, data.PodURL)
			lw.database.SaveRequestLatency(data.PodURL, data.NodeIP, lw.hostNodeIP, data.Latency)
		}
	}()
}

func (lw *LatencyWorker) SendLatencyData(data LatencyData) {
	lw.latencyChan <- data
}
