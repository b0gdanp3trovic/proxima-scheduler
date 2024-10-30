package scheduler

import (
	"fmt"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	"k8s.io/client-go/kubernetes"
)

type ScoresWorker struct {
	Clientset *kubernetes.Clientset
	Scores    map[string]float64
	DbScores  util.Database
	DbPing    util.Database
}

func NewScoresWorker(clientset *kubernetes.Clientset, dbScores util.Database, dbPing util.Database) *ScoresWorker {
	return &ScoresWorker{
		Clientset: clientset,
		Scores:    make(map[string]float64),
		DbScores:  dbScores,
		DbPing:    dbPing,
	}
}

func (sw *ScoresWorker) processNodes() {
	_, err := util.DiscoverNodes(sw.Clientset)
	if err != nil {
		fmt.Printf("Error processing nodes: %v\n", err)
		return
	}

	// TODO: implement scoring system
}
