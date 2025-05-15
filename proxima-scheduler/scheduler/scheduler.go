package scheduler

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type PodLocation struct {
	Cluster string
	NodeIP  string
	Status  string
}

type TrackedPods map[string]map[string]PodLocation

type Scheduler struct {
	Clientsets         map[string]*kubernetes.Clientset
	SchedulerName      string
	IncludedNamespaces []string
	StopCh             chan struct{}
	DB                 util.Database
	ScheduledPods      map[string]map[string]string
	EdgeProxies        []string
	TrackedPods        TrackedPods
	PodMutex           sync.RWMutex
}

type nodeScorePair struct {
	Name  string
	Score float64
}

func NewScheduler(
	schedulerName string,
	includedNamespaces []string,
	edgeProxies []string,
	inClusterClientset *kubernetes.Clientset,
	kubeconfigs map[string]string,
	db util.Database) (*Scheduler, error) {
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
		TrackedPods:        make(TrackedPods),
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

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.ReconcilePods()
			case <-s.StopCh:
				log.Println("Stopping reconciliation loop")
				return
			}
		}
	}()
}

func (s *Scheduler) schedulePod(pod *v1.Pod) {
	nodeScores, err := s.GetNodeScores()

	if err != nil {
		log.Printf("Error obtaining node scores: %v", err)
	}

	targetNodeIP, targetCluster, err := s.GetNodeIPForSchedule(nodeScores, pod)
	if err != nil {
		log.Printf("Error selecting node for pod %s: %v\n", pod.GetName(), err)
		return
	}

	node, err := util.GetNodeByInternalIP(s.Clientsets[targetCluster], targetNodeIP)
	if err != nil {
		log.Printf("Failed to get node for IP %s: %v", targetNodeIP, err)
		return
	}
	nodeName := node.Name

	// Best node in local cluster, schedule here and exit
	if targetCluster == "local" {
		log.Printf("Scheduling pod %s in local cluster via binding to node %s", pod.Name, nodeName)
		s.bindPodToNode(s.Clientsets["local"], pod, nodeName)
		return
	}

	// Remote cluster, perform a deep copy and schedule
	podCopy := pod.DeepCopy()
	podCopy.ResourceVersion = ""
	podCopy.UID = ""
	podCopy.Spec.NodeName = nodeName
	podCopy.Name = fmt.Sprintf("%s-scheduled", pod.Name)

	log.Printf("Creating pod %s in cluster %s on node %s", podCopy.Name, targetCluster, nodeName)
	_, err = s.Clientsets[targetCluster].CoreV1().Pods(pod.Namespace).Create(context.TODO(), podCopy, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating pod in cluster %s: %v", targetCluster, err)
		return
	}

	err = s.Clientsets["local"].CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
	if err != nil {
		log.Printf("Failed to delete original pod %s: %v", pod.Name, err)
	} else {
		log.Printf("Deleted original pod %s from local cluster", pod.Name)
	}

	app := pod.Labels["app"]
	if app == "" {
		app = "unknown"
	}

	s.PodMutex.Lock()
	if _, ok := s.TrackedPods[app]; !ok {
		s.TrackedPods[app] = make(map[string]PodLocation)
	}
	s.TrackedPods[app][podCopy.Name] = PodLocation{
		Cluster: targetCluster,
		NodeIP:  targetNodeIP,
		Status:  "Scheduled",
	}
	s.PodMutex.Unlock()
}

func (s *Scheduler) ReconcilePods() {
	newState := make(TrackedPods)

	for clusterName, clientset := range s.Clientsets {
		for _, namespace := range s.IncludedNamespaces {
			pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
				LabelSelector: "app",
			})

			if err != nil {
				log.Printf("Error listing pods in cluster %s and namespace %s: %v", clusterName, namespace, err)
			}

			for _, pod := range pods.Items {
				if pod.Spec.NodeName == "" {
					continue
				}

				app := pod.Labels["app"]
				if app == "" {
					log.Printf("App label not found on pod %v, using unknown.", pod.Name)
				}

				if _, ok := newState[app]; !ok {
					newState[app] = make(map[string]PodLocation)
				}

				node, err := clientset.CoreV1().Nodes().Get(context.TODO(), pod.Spec.NodeName, metav1.GetOptions{})
				if err != nil {
					log.Printf("Error obtaining node %s", pod.Spec.NodeName)
				}

				nodeIP, err := util.GetNodeInternalIP(node)
				if err != nil {
					log.Printf("Error obtaining NodeIP for node %s", pod.Spec.NodeName)
				}

				newState[app][pod.Name] = PodLocation{
					Cluster: clusterName,
					NodeIP:  nodeIP,
				}
			}
		}
	}

	s.PodMutex.Lock()
	s.TrackedPods = newState
	s.PodMutex.Unlock()

	log.Printf("Updated pod state. %d apps being tracked.", len(newState))
	log.Printf("Current state: %v", newState)
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
		nodeList, err := util.DiscoverNodes(clientset, true)
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
