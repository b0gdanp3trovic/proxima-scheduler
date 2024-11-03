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
	DbScores               util.Database
	DbPing                 util.Database
	ScoringInterval        time.Duration
}

func NewScoresWorker(clientset *kubernetes.Clientset, dbScores util.Database, dbPing util.Database, scoringInterval time.Duration) *ScoresWorker {
	return &ScoresWorker{
		Clientset:              clientset,
		Scores:                 make(map[string]float64),
		EdgeWeights:            make(map[string]float64),
		EdgeWeightsInitialized: false,
		DbScores:               dbScores,
		DbPing:                 dbPing,
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
	fmt.Println("Edge weights initialized:", sw.EdgeWeights)
	return nil
}

func (sw *ScoresWorker) scoreNodes() {
	nodeLatenciesByEdgeProxy, err := sw.DbPing.GetAveragePingTimeByEdges()
	if err != nil {
		fmt.Printf("Error obtaining ping times: %v\n", err)
		return
	}

	if sw.EdgeWeightsInitialized {
		sw.initializeEdgeWeights(nodeLatenciesByEdgeProxy)
	}

	for edgeProxy, latencies := range nodeLatenciesByEdgeProxy {
		edgeProxyWeight := sw.EdgeWeights[edgeProxy]

		for nodeIP, latency := range latencies {
			score := 1 - edgeProxyWeight*latency

			sw.Scores[nodeIP] = score
		}
	}

	err = sw.DbScores.SaveNodeScores(sw.Scores)
	if err != nil {
		fmt.Printf("Error saving node scores: %v\n", err)
	}
}

func (sw *ScoresWorker) Start() {
	log.Printf("Starting scores worker...")
	ticker := time.NewTicker(sw.ScoringInterval)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			log.Println("Scoring nodes...")
			sw.scoreNodes()
		}
	}()
}
