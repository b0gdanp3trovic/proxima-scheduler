package edgeproxy

import (
	"log"
	"time"
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
}

func NewLatencyWorker(bufferSize int) *LatencyWorker {
	latencyChan := make(chan LatencyData, bufferSize)
	return &LatencyWorker{
		latencyChan: latencyChan,
	}
}

func (lw *LatencyWorker) Start() {
	log.Printf("Starting latency worker...")
	go func() {
		for data := range lw.latencyChan {
			// TODO: save to db
			log.Printf("Processing latency data: %v for pod: %s", data.Latency, data.PodURL)
		}
	}()
}

func (lw *LatencyWorker) SendLatencyData(data LatencyData) {
	lw.latencyChan <- data
}
