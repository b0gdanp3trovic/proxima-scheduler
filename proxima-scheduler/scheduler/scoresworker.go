package scheduler

import (
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

//func (sw *ScoresWorker) scoreNodes() {
//	nodes, err := util.DiscoverNodes(sw.Clientset)
//	if err != nil {
//		fmt.Printf("Error processing nodes: %v\n", err)
//		return
//	}
//
//	for _, node := range nodes.Items {
//		address := node.Status.Addresses[0].Address
//	}
//
//	// TODO: implement scoring system
//}
