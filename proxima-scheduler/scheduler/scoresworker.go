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
	EdgeProxies            []string
}

func NewScoresWorker(clientset *kubernetes.Clientset, db *util.InfluxDB, scoringInterval time.Duration, edgeProxies []string) *ScoresWorker {
	return &ScoresWorker{
		Clientset:              clientset,
		Scores:                 make(map[string]float64),
		EdgeWeights:            make(map[string]float64),
		EdgeWeightsInitialized: false,
		Db:                     db,
		ScoringInterval:        scoringInterval,
		EdgeProxies:            edgeProxies,
	}
}

func (sw *ScoresWorker) calculateEdgeWeights(nodeLatenciesByEdgeProxy util.EdgeProxyToNodeLatencies) error {
	rpmByEdge, err := sw.Db.GetTotalRPMByEdgeProxy()
	if err != nil || len(rpmByEdge) == 0 {
		log.Printf("Warning: using equal weights due to missing or failed RPM data: %v", err)
		return sw.initializeEqualEdgeWeights(nodeLatenciesByEdgeProxy)
	}

	var totalRPM float64
	for _, rpm := range rpmByEdge {
		totalRPM += rpm
	}
	if totalRPM == 0 {
		log.Println("falling back to equal weights")
		return sw.initializeEqualEdgeWeights(nodeLatenciesByEdgeProxy)
	}

	for edgeProxy, rpm := range rpmByEdge {
		sw.EdgeWeights[edgeProxy] = rpm / totalRPM
	}

	sw.EdgeWeightsInitialized = true
	log.Printf("Edge weights initialized based on RPM: %v\n", sw.EdgeWeights)
	return nil
}

func (sw *ScoresWorker) initializeEqualEdgeWeights(nodeLatenciesByEdgeProxy util.EdgeProxyToNodeLatencies) error {
	numEdges := len(nodeLatenciesByEdgeProxy)
	if numEdges == 0 {
		return fmt.Errorf("no edge proxies found to initialize weights")
	}

	equalWeight := 1.0 / float64(numEdges)
	for edgeProxy := range nodeLatenciesByEdgeProxy {
		sw.EdgeWeights[edgeProxy] = equalWeight
	}

	sw.EdgeWeightsInitialized = true
	log.Printf("Edge weights initialized equally: %v\n", sw.EdgeWeights)
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
		if err := sw.calculateEdgeWeights(nodeLatenciesByEdgeProxy); err != nil {
			log.Printf("Error initializing edge weights: %v\n", err)
			return
		}
	}

	weightedLatencies := make(map[string]float64)
	weightSums := make(map[string]float64)
	Lmin, Lmax := math.MaxFloat64, 0.0

	for edgeProxy, latencies := range nodeLatenciesByEdgeProxy {
		edgeProxyWeight := sw.EdgeWeights[edgeProxy]

		for nodeIP, latency := range latencies {
			if util.IsEdgeProxy(nodeIP, sw.EdgeProxies) {
				log.Printf("Found edge proxy %s. Skipping", nodeIP)
				continue
			}

			if latency < Lmin {
				Lmin = latency
			}

			if latency > Lmax {
				Lmax = latency
			}

			weightedLatencies[nodeIP] += edgeProxyWeight * latency
			weightSums[nodeIP] += edgeProxyWeight
		}
	}

	if Lmax == Lmin {
		log.Println("No variance, skipping.")
		return
	}

	for nodeIP, weightedLatency := range weightedLatencies {
		finalLatency := weightedLatency / weightSums[nodeIP]

		// Sigmoid
		score := 1.0 / (1.0 + math.Exp(0.007*(finalLatency-Lmin)))
		sw.Scores[nodeIP] = score

		log.Printf("Node %s - Latency: %.2f ms, Score: %.4f\n", nodeIP, finalLatency, sw.Scores[nodeIP])
	}

	err = sw.Db.SaveNodeScores(sw.Scores)
	if err != nil {
		log.Printf("Error saving node scores: %v\n", err)
	}

	log.Println("Saved node scores.")
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
