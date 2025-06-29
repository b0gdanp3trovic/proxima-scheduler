package scheduler

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type Scheduler struct {
	Clientsets         map[string]*kubernetes.Clientset
	SchedulerName      string
	IncludedNamespaces []string
	StopCh             chan struct{}
	DB                 util.Database
	ScheduledPods      map[string]map[string]string
	EdgeProxies        []string
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
								// Skip generated pod, as this is already a re-schedule.
								// We avoid infinite scheduling cycles like this.
								if pod.Annotations["proxima-scheduler/generated"] == "true" {
									log.Printf("Skipping generated pod %s", pod.Name)
									s.schedulePod(pod)
									return
								}

								log.Printf("New pod detected in cluster %s, scheduling...\n", clusterName)

								if replicaStr, ok := pod.Annotations["proxima-scheduler/replicas"]; ok {
									replicas, err := strconv.Atoi(replicaStr)
									if err != nil {
										log.Printf("Invalid replicas annotation on pod %s: %v", pod.Name, err)
									}

									for i := 0; i < replicas; i++ {
										copy := pod.DeepCopy()
										copy.ResourceVersion = ""
										copy.UID = ""
										copy.Spec.NodeName = ""
										copy.Name = fmt.Sprintf("%s-%d", pod.Name, i)
										copy.Annotations["proxima-scheduler/generated"] = "true"
										copy.Annotations["proxima-scheduler/generated-from"] = pod.Name
										_, err := s.Clientsets["local"].CoreV1().Pods(pod.Namespace).Create(context.TODO(), copy, metav1.CreateOptions{})

										if err != nil {
											log.Printf("Failed to create replica pod %s: %v", copy.Name, err)
										} else {
											log.Printf("Created replica pod %s", copy.Name)
										}
									}

									_ = s.Clientsets["local"].CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
									return
								}
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
	// Clanky that we need to copy multiple times. TODO - rethink this
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
}

/*
Function that is performed periodically, update state and check if there is a possible better node for a pod.
TODO - think about latency limit, decouple this function.
*/
func (s *Scheduler) ReconcilePods() {
	nodeScores, err := s.GetNodeScores()
	if err != nil {
		log.Printf("Failed to get node scores during reconciliation: %v", err)
		return
	}

	edgeToLatencies := s.obtainEdgeNodeLatencies()

	for clusterName, clientset := range s.Clientsets {
		for _, namespace := range s.IncludedNamespaces {
			pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
				LabelSelector: "app",
			})

			if err != nil {
				log.Printf("Error listing pods in cluster %s and namespace %s: %v", clusterName, namespace, err)
			}

			for _, pod := range pods.Items {
				limitStr, hasLimit := pod.Annotations["proxima-scheduler/latency_limit"]
				var latencyLimit time.Duration
				if hasLimit {
					var err error
					latencyLimit, err = time.ParseDuration(limitStr)
					if err != nil {
						log.Printf("Invalid latency_limit annotation on pod %s: %v", pod.Name, err)
						hasLimit = false
					}
				}

				if pod.Spec.NodeName == "" {
					continue
				}

				if pod.DeletionTimestamp != nil {
					log.Printf("Skipping pod %s because it's being deleted", pod.Name)
					continue
				}

				app := pod.Labels["app"]
				if app == "" {
					log.Printf("App label not found on pod %v, using unknown.", pod.Name)
				}

				node, err := clientset.CoreV1().Nodes().Get(context.TODO(), pod.Spec.NodeName, metav1.GetOptions{})
				if err != nil {
					log.Printf("Error obtaining node %s", pod.Spec.NodeName)
				}

				currentNodeIP, err := util.GetNodeInternalIP(node)
				if err != nil {
					log.Printf("Error obtaining NodeIP for node %s", pod.Spec.NodeName)
				}

				if hasLimit {
					meetsLimit := true

					for edgeProxy, latencies := range edgeToLatencies {
						latencyMs, ok := latencies[currentNodeIP]

						if !ok {
							log.Printf("Pod %s: missing latency data from edge %s to node %s", pod.Name, edgeProxy, currentNodeIP)
							meetsLimit = false
							break
						}

						latencyDuration := time.Duration(latencyMs * float64(time.Millisecond))
						if latencyDuration > latencyLimit {
							log.Printf("Pod %s: current node exceeds latency limit from edge %s: %v>%v",
								pod.Name, edgeProxy, latencyDuration, latencyLimit)
							meetsLimit = false
							break
						}
					}

					if !meetsLimit {
						log.Printf("Pod %s no longer meets latency constraint, rescheduling...", pod.Name)

						bestIP, bestCluster, err := s.GetNodeIPForSchedule(nodeScores, &pod)
						if err != nil {
							log.Printf("Error finding new node for pod %s: %v", pod.Name, err)
							continue
						}

						bestNode, err := util.GetNodeByInternalIP(s.Clientsets[bestCluster], bestIP)
						if err != nil {
							log.Printf("Failed to get node object for IP %s: %v", bestIP, err)
							continue
						}
						nodeName := bestNode.Name

						newPod := pod.DeepCopy()
						newPod.ResourceVersion = ""
						newPod.UID = ""
						newPod.Spec.NodeName = nodeName
						newPod.Name = withNewHashedName(pod.Name)

						_, err = s.Clientsets[bestCluster].CoreV1().Pods(newPod.Namespace).Create(context.TODO(), newPod, metav1.CreateOptions{})
						if err != nil {
							log.Printf("Failed to create rescheduled pod %s in cluster %s: %v", newPod.Name, bestCluster, err)
							continue
						}

						err = clientset.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
						if err != nil {
							log.Printf("Failed to delete original pod %s: %v", pod.Name, err)
						} else {
							log.Printf("Rescheduled pod %s to node %s (%s) due to latency SLO violation.", pod.Name, nodeName, bestCluster)
						}

						// Don't continue with the rescheduling
						continue
					}
				}

				//TODO: throttle rescheduling
				currentScore := nodeScores[clusterName][currentNodeIP]
				bestIP, bestCluster, err := s.GetNodeIPForSchedule(nodeScores, &pod)

				if err != nil {
					log.Printf("Error getting best node for pod %s: %v", pod.Name, err)
					continue
				}

				bestScore := nodeScores[bestCluster][bestIP]

				if bestScore > currentScore*1.2 {
					log.Printf("Pod %s is on a suboptimal node. Best score: %.2f vs current %.2f. Rescheduling...", pod.Name, bestScore, currentScore)

					node, err := util.GetNodeByInternalIP(s.Clientsets[bestCluster], bestIP)
					if err != nil {
						log.Printf("Failed to get best node for IP %s: %v", bestIP, err)
						continue
					}
					nodeName := node.Name

					// Another copy - rethink it ASAP
					newPod := pod.DeepCopy()
					newPod.ResourceVersion = ""
					newPod.UID = ""
					newPod.Spec.NodeName = nodeName
					newPod.Name = withNewHashedName(pod.Name)

					_, err = s.Clientsets[bestCluster].CoreV1().Pods(newPod.Namespace).Create(context.TODO(), newPod, metav1.CreateOptions{})
					if err != nil {
						log.Printf("Failed to create rescheduled pod %s in cluster %s: %v", newPod.Name, bestCluster, err)
						continue
					}

					err = clientset.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
					if err != nil {
						log.Printf("Failed to delete original pod %s from cluster %s: %v", pod.Name, clusterName, err)
					} else {
						log.Printf("Successfully rescheduled pod %s to %s (%s)", pod.Name, nodeName, bestCluster)
					}
				} else {
					log.Printf("Pod %s is on an optimal node: - %s", pod.Name, pod.Spec.NodeName)
				}
			}
		}
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

// Regex for stripping out the existing hash we are replacing.
var hashSuffix = regexp.MustCompile(`-[a-f0-9]{8}$`)

func withNewHashedName(podName string) string {
	base := hashSuffix.ReplaceAllString(podName, "")
	return fmt.Sprintf("%s-%s", base, calculateNewHash(base))
}

func calculateNewHash(podName string) string {
	hasher := sha1.New()
	hasher.Write([]byte(podName + time.Now().Format(time.RFC3339Nano)))
	sum := hasher.Sum(nil)
	return hex.EncodeToString(sum)[:8]
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
			if !isNodeReady(&node) || !isSchedulable(&node) {
				log.Printf("Skipping node %s in cluster %s (NotReady or tainted)", node.Name, clusterName)
				continue
			}

			var nodeIP string

			// We already have a function for this
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
	limitStr, hasLimit := pod.Annotations["proxima-scheduler/latency_limit"]
	var latencyLimit time.Duration
	if hasLimit {
		var err error
		latencyLimit, err = time.ParseDuration(limitStr)
		if err != nil {
			log.Printf("Invalid latency_limit annotation on pod %s: %v", pod.Name, err)
			return "", "", fmt.Errorf("invalid latency_limit: %w", err)
		}
	}

	edgeToLatencies := s.obtainEdgeNodeLatencies()

	// Based on score only
	// Any other metrics?
	bestFreeNode := ""
	bestFreeNodeCluster := ""
	bestScore := -math.MaxFloat64

	for clusterName, nodes := range nodeScores {
		for nodeIP, score := range nodes {
			if !hasEnoughCapacity(s.Clientsets[clusterName], nodeIP, pod) {
				continue
			}

			if hasLimit {
				meetsLimit := true

				for edgeProxy, latencies := range edgeToLatencies {
					latencyMs, ok := latencies[nodeIP]
					if !ok {
						log.Printf("Node %s missing latency data from edge %s", nodeIP, edgeProxy)
						meetsLimit = false
						break
					}

					latencyDuration := time.Duration(latencyMs * float64(time.Millisecond))
					if latencyDuration > latencyLimit {
						log.Printf("Node %s exceeds latency limit from edge %s: %v > %v", nodeIP, edgeProxy, latencyDuration, latencyLimit)
						meetsLimit = false
						break
					}
				}

				if !meetsLimit {
					continue
				}
			}

			if score > bestScore {
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

func (s *Scheduler) obtainEdgeNodeLatencies() map[string]util.NodeLatencies {
	edgeToLatencies := make(map[string]util.NodeLatencies)

	for _, edgeProxy := range s.EdgeProxies {
		latencies, err := s.DB.GetAverageLatenciesForEdge(edgeProxy)
		if err != nil {
			log.Printf("Failed to get latencies from edge %s: %v", edgeProxy, err)
			continue
		}
		edgeToLatencies[edgeProxy] = latencies
	}

	return edgeToLatencies
}

func isNodeReady(node *v1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == v1.NodeReady {
			return condition.Status == v1.ConditionTrue
		}
	}
	return false
}

func isSchedulable(node *v1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Effect == v1.TaintEffectNoSchedule {
			return false
		}
	}
	return true
}
