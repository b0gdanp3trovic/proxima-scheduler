package scheduler

import (
	"context"
	"fmt"
	"log"
	"math"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type DesiredStateResult map[string]map[string]string

type Scheduler struct {
	Clientsets         map[string]*kubernetes.Clientset
	SchedulerName      string
	IncludedNamespaces []string
	StopCh             chan struct{}
	DB                 util.Database
	ScheduledPods      map[string]map[string]string
	EdgeProxies        []string
}

func NewScheduler(schedulerName string, includedNamespaces []string, edgeProxies []string, inClusterClientset *kubernetes.Clientset, kubeconfigs map[string]string, db util.Database) (*Scheduler, error) {
	clientsets := make(map[string]*kubernetes.Clientset)

	for clusterName, kubeconfigPath := range kubeconfigs {
		clientset, err := util.GetClientsetForCluster(kubeconfigPath)
		if err != nil {
			log.Printf("Failed to load cluster %s: %v\n", clusterName, err)
			return nil, err
		}
		clientsets[clusterName] = clientset
	}

	clientsets["local"] = inClusterClientset

	if len(clientsets) == 0 {
		return nil, fmt.Errorf("No Kubernetes clusters available")
	}

	return &Scheduler{
		Clientsets:         clientsets,
		SchedulerName:      schedulerName,
		IncludedNamespaces: includedNamespaces,
		StopCh:             make(chan struct{}),
		DB:                 db,
		EdgeProxies:        edgeProxies,
	}, nil
}

func (s *Scheduler) Run() {
	log.Printf("Clientsets: %v", s.Clientsets)
	for clusterName, clientset := range s.Clientsets {
		for _, ns := range s.IncludedNamespaces {
			go func(clusterName, namespace string, clientset *kubernetes.Clientset) {
				log.Printf("Watching pods in namespace %s on cluster %s\n", namespace, clusterName)

				podListWatcher := cache.NewListWatchFromClient(
					clientset.CoreV1().RESTClient(),
					"pods",
					namespace,
					fields.Everything(),
				)

				_, controller := cache.NewInformer(
					podListWatcher,
					&v1.Pod{},
					0,
					cache.ResourceEventHandlerFuncs{
						AddFunc: func(obj interface{}) {
							pod := obj.(*v1.Pod)
							if pod.Spec.SchedulerName == s.SchedulerName && pod.Spec.NodeName == "" {
								log.Printf("New pod detected in cluster %s, scheduling...\n", clusterName)
								s.schedulePod(pod)
							}
						},
					},
				)

				controller.Run(s.StopCh)
			}(clusterName, ns, clientset)
		}
	}
}

func (s *Scheduler) schedulePod(pod *v1.Pod) {
	nodeScores, err := s.GetNodeScores()
	if err != nil {
		log.Printf("Error obtaining node scores: %v\n", err)
	}

	if err != nil {
		log.Printf("Error listing nodes: %v\n", err)
		return
	}

	targetNodeIP, clusterName, err := s.GetNodeIPForSchedule(nodeScores, pod)

	if err != nil {
		log.Printf("Error selecting node for pod %s: %v\n", pod.GetName(), err)
	}

	if targetNodeIP != "" {
		log.Printf("Scheduling pod %s to node %s\n", pod.GetName(), targetNodeIP)
		// Bind the pod to the selected node
		s.bindPodToNode(s.Clientsets[clusterName], pod, targetNodeIP)
	}
}

func (s *Scheduler) bindPodToNode(clientset *kubernetes.Clientset, pod *v1.Pod, nodeName string) {
	binding := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: pod.Namespace,
			Name:      pod.Name,
			UID:       pod.UID,
		},
		Target: v1.ObjectReference{
			Kind: "Node",
			Name: nodeName,
		},
	}

	err := clientset.CoreV1().Pods(pod.Namespace).Bind(context.TODO(), binding, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error binding pod: %v\n", err)
		return
	}

	log.Printf("Successfully scheduled pod %s to node %s.\n", pod.GetName(), nodeName)
}

func (s *Scheduler) GetNodeScores() (map[string]map[string]float64, error) {
	nodeScores := make(map[string]map[string]float64)

	for clusterName, clientset := range s.Clientsets {
		nodeList, err := util.DiscoverNodes(clientset, s.EdgeProxies)
		if err != nil {
			return nil, fmt.Errorf("failed to list nodes in cluster %s: %w", clusterName, err)
		}

		nodeScores[clusterName] = make(map[string]float64)

		for _, node := range nodeList.Items {
			var nodeIP string

			for _, addr := range node.Status.Addresses {
				if addr.Type == v1.NodeInternalIP {
					nodeIP = addr.Address
				}
			}

			nodeScores[clusterName][nodeIP], err = s.DB.GetNodeScore(nodeIP)
			if err != nil {
				return nil, fmt.Errorf("failed to obtain node score for node %s: %w", nodeIP, err)
			}
		}
	}

	return nodeScores, nil
}

func (s *Scheduler) GetNodeIPForSchedule(nodeScores map[string]map[string]float64, pod *v1.Pod) (string, string, error) {
	// Based on score only
	bestFreeNode := ""
	bestFreeNodeCluster := ""
	bestScore := -math.MaxFloat64

	for clusterName, nodes := range nodeScores {
		for nodeIP, score := range nodes {
			if score > bestScore && hasEnoughCapacity(s.Clientsets[clusterName], nodeIP, pod) {
				bestScore = score
				bestFreeNode = nodeIP
				bestFreeNodeCluster = clusterName
			}
		}
	}

	if bestFreeNode == "" {
		return "", "", fmt.Errorf("no suitable node found for scheduling pod %s", pod.Name)
	}

	return bestFreeNode, bestFreeNodeCluster, nil
}
