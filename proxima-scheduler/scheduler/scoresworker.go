package scheduler

import (
	"fmt"
	"log"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	"k8s.io/client-go/kubernetes"
)

type ScoresWorker struct {
	Clientset              *kubernetes.Clientset
	Scores                 map[string]float64
	EdgeWeights            map[string]float64
	EdgeWeightsInitialized bool
	Db                     util.Database
	ScoringInterval        time.Duration
}

func NewScoresWorker(clientset *kubernetes.Clientset, db *util.InfluxDB, scoringInterval time.Duration) *ScoresWorker {
	return &ScoresWorker{
		Clientset:              clientset,
		Scores:                 make(map[string]float64),
		EdgeWeights:            make(map[string]float64),
		EdgeWeightsInitialized: false,
		Db:                     db,
		ScoringInterval:        scoringInterval,
	}
}

func (sw *ScoresWorker) initializeEdgeWeights(nodeLatenciesByEdgeProxy util.EdgeProxyToNodeLatencies) error {
	numEdges := len(nodeLatenciesByEdgeProxy)
	if numEdges == 0 {
		return fmt.Errorf("no edge proxies found")
	}

	equalWeight := 1.0 / float64(numEdges)

	for edgeProxy := range nodeLatenciesByEdgeProxy {
		sw.EdgeWeights[edgeProxy] = equalWeight
	}

	sw.EdgeWeightsInitialized = true
	log.Printf("Edge weights initialized: %v\n", sw.EdgeWeights)
	return nil
}

func (sw *ScoresWorker) scoreNodes() {
	nodeLatenciesByEdgeProxy, err := sw.Db.GetAveragePingTimeByEdges()
	if err != nil {
		log.Printf("Error obtaining ping times: %v\n", err)
		return
	}
	log.Printf("Fetched node latencies by edge proxy: %v\n", nodeLatenciesByEdgeProxy)

	if !sw.EdgeWeightsInitialized {
		if err := sw.initializeEdgeWeights(nodeLatenciesByEdgeProxy); err != nil {
			log.Printf("Error initializing edge weights: %v\n", err)
			return
		}
	}

	for edgeProxy, latencies := range nodeLatenciesByEdgeProxy {
		edgeProxyWeight, exists := sw.EdgeWeights[edgeProxy]
		if !exists {
			log.Printf("Warning: Edge proxy %s has no initialized weight, skipping...", edgeProxy)
			continue
		}

		for nodeIP, latency := range latencies {
			score := 1 - edgeProxyWeight*latency
			sw.Scores[nodeIP] = score
			log.Printf("Calculated score for node %s via edge proxy %s: %f\n", nodeIP, edgeProxy, score)
		}
	}

	err = sw.Db.SaveNodeScores(sw.Scores)
	if err != nil {
		fmt.Printf("Error saving node scores: %v\n", err)
	}

	fmt.Printf("Saved node scores.")
}

func (sw *ScoresWorker) Run() {
	log.Printf("Starting scores worker...")

	go func() {
		ticker := time.NewTicker(sw.ScoringInterval)
		defer ticker.Stop()

		for range ticker.C {
			log.Println("Scoring nodes...")
			sw.scoreNodes()
		}
	}()
}
