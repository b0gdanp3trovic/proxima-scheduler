package scheduler

import (
	"fmt"
	"log"
	"math"
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

	rawLatencies := make(map[string]float64)

	for edgeProxy, latencies := range nodeLatenciesByEdgeProxy {
		edgeProxyWeight, exists := sw.EdgeWeights[edgeProxy]
		if !exists {
			log.Printf("Warning: Edge proxy %s has no initialized weight, skipping...", edgeProxy)
			continue
		}

		for nodeIP, latency := range latencies {
			rawLatencies[nodeIP] += edgeProxyWeight * latency
		}
	}

	minLatency, maxLatency := findMinMax(rawLatencies)
	for nodeIP, latency := range rawLatencies {
		if maxLatency > minLatency {
			normalizedScore := 1 - ((latency - minLatency) / (maxLatency - minLatency))
			sw.Scores[nodeIP] = normalizedScore
			log.Printf("Normalized score for node %s: %f\n", nodeIP, normalizedScore)
		} else {
			sw.Scores[nodeIP] = 1
		}
	}

	err = sw.Db.SaveNodeScores(sw.Scores)
	if err != nil {
		fmt.Printf("Error saving node scores: %v\n", err)
	}

	fmt.Printf("Saved node scores.")
}

func findMinMax(values map[string]float64) (float64, float64) {
	min := math.MaxFloat64
	max := -math.MaxFloat64
	for _, value := range values {
		if value < min {
			min = value
		}
		if value > max {
			max = value
		}
	}
	return min, max
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
