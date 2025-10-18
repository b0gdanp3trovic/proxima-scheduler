package scheduler

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
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
	EdgeProxies        []string
	DesiredApps        map[string]*DesiredApp
}

type DesiredApp struct {
	App         string
	Namespace   string
	Replicas    int
	Annotations map[string]string
	Labels      map[string]string
	Template    *v1.Pod
	PodsByName  map[string]*DesiredPod
}

type DesiredPod struct {
	Name     string
	Cluster  string
	NodeName string
}

type NodeCandidate struct {
	ip, cluster string
	score       float64
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
		DesiredApps:        make(map[string]*DesiredApp),
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

									s.DesiredApps[pod.Labels["app"]] = &DesiredApp{
										App:         pod.Labels["app"],
										Namespace:   pod.Namespace,
										Replicas:    replicas,
										Annotations: pod.Annotations,
										Labels:      pod.Labels,
										Template:    pod.DeepCopy(),
										PodsByName:  make(map[string]*DesiredPod),
									}

									for i := 0; i < replicas; i++ {
										copy := pod.DeepCopy()
										copy.ResourceVersion = ""
										copy.UID = ""
										copy.Spec.NodeName = ""
										copy.Name = withNewHashedName(fmt.Sprintf("%s-%d", pod.Name, i))
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
				s.EnforceDesired()
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
	podCopy.Name = withNewHashedName(pod.Name)

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

	app := podCopy.Labels["app"]

	s.DesiredApps[app].PodsByName[podCopy.Name] = &DesiredPod{
		Name:     podCopy.Name,
		Cluster:  targetCluster,
		NodeName: podCopy.Spec.NodeName,
	}
}

func (s *Scheduler) reschedulePod(
	pod *v1.Pod,
	nodeScores map[string]map[string]float64,
	currentCluster string,
	clientset *kubernetes.Clientset,
) {
	bestIP, bestCluster, err := s.GetNodeIPForSchedule(nodeScores, pod)
	if err != nil {
		log.Printf("Error finding new node for pod %s: %v", pod.Name, err)
		return
	}

	bestNode, err := util.GetNodeByInternalIP(s.Clientsets[bestCluster], bestIP)
	if err != nil {
		log.Printf("Failed to get node object for IP %s: %v", bestIP, err)
		return
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
		return
	}

	err = clientset.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
	if err != nil {
		log.Printf("Failed to delete original pod %s: %v", pod.Name, err)
	} else {
		log.Printf("Rescheduled pod %s to node %s (%s)", pod.Name, nodeName, bestCluster)
	}
}

func (s *Scheduler) EnforceDesired() {
	for appName, desired := range s.DesiredApps {
		runningCount := 0
		foundNames := map[string]bool{}

		for clusterName, clientset := range s.Clientsets {
			pods, err := clientset.CoreV1().Pods(desired.Namespace).List(context.TODO(), metav1.ListOptions{
				LabelSelector: fmt.Sprintf("app=%s", appName),
			})

			if err != nil {
				log.Printf("Error listing pods for app %s in cluster %s: %v", appName, clusterName, err)
				continue
			}

			for _, pod := range pods.Items {
				if pod.DeletionTimestamp != nil {
					continue
				}

				if pod.Spec.SchedulerName != s.SchedulerName {
					continue
				}

				switch pod.Status.Phase {
				case v1.PodRunning:
					runningCount++
					foundNames[pod.Name] = true

				case v1.PodPending:
					pendingDuration := time.Since(pod.CreationTimestamp.Time)
					if pendingDuration > 60*time.Second {
						log.Printf("Pending pod %s for %v deleting and replacing...", pod.Name, pendingDuration)
						err := clientset.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
						if err != nil {
							log.Printf("Error deleting stuck pending pod %s: %v", pod.Name, err)
						}
					} else {
						runningCount++
						foundNames[pod.Name] = true
						log.Printf("Pending pod %s is new (%v) — counted as temporarily available", pod.Name, pendingDuration)
					}
				case v1.PodFailed:
					log.Printf("Pod %s is failed — deleting and replacing", pod.Name)
					err := clientset.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
					if err != nil {
						log.Printf("Error deleting failed pod %s: %v", pod.Name, err)
					}

				default:
					log.Printf("Pod %s in phase %s — not counted", pod.Name, pod.Status.Phase)
				}

				if pod.Status.Phase != v1.PodRunning {
					continue
				}
			}
		}

		for name := range desired.PodsByName {
			if !foundNames[name] {
				log.Printf("Tracked pod %s for app %s is missing from cluster state", name, appName)
				delete(desired.PodsByName, name)
				s.spawnReplica(desired)
			}
		}
	}
}

func (s *Scheduler) spawnReplica(desired *DesiredApp) {
	copy := desired.Template.DeepCopy()
	copy.ResourceVersion = ""
	copy.UID = ""
	copy.Spec.NodeName = ""
	copy.Name = withNewHashedName(desired.Template.Name)

	if copy.Annotations == nil {
		copy.Annotations = map[string]string{}
	}
	copy.Annotations["proxima-scheduler/generated"] = "true"
	copy.Annotations["proxima-scheduler/generated-from"] = desired.Template.Name

	_, err := s.Clientsets["local"].CoreV1().Pods(desired.Namespace).Create(context.TODO(), copy, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Failed to spawn replica pod %s: %v", copy.Name, err)
		return
	}

	log.Printf("Spawned missing replica %s for app %s", copy.Name, desired.App)
}

func (s *Scheduler) handlePodReconciliation(
	pod v1.Pod,
	nodeScores map[string]map[string]float64,
	edgeToLatencies map[string]util.NodeLatencies,
	clusterName string,
	clientset *kubernetes.Clientset,
) {
	limitStr, hasLimit := pod.Annotations["proxima-scheduler/max-latency-ms"]
	var latencyLimit time.Duration
	if hasLimit {
		ms, err := strconv.ParseInt(limitStr, 10, 64)
		if err != nil {
			log.Printf("Invalid max-latency-ms annotation on pod %s: %v", pod.Name, err)
			hasLimit = false
		} else {
			latencyLimit = time.Duration(ms) * time.Millisecond
		}
	}

	if pod.Spec.NodeName == "" {
		return
	}

	if pod.DeletionTimestamp != nil {
		log.Printf("Skipping pod %s because it's being deleted", pod.Name)
		return
	}

	node, err := clientset.CoreV1().Nodes().Get(context.TODO(), pod.Spec.NodeName, metav1.GetOptions{})
	if err != nil {
		log.Printf("Error obtaining node %s: %v", pod.Spec.NodeName, err)
		return
	}

	currentNodeIP, err := util.GetNodeInternalIP(node)
	if err != nil {
		log.Printf("Error obtaining NodeIP for node %s: %v", pod.Spec.NodeName, err)
		return
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
				log.Printf("Pod %s: current node exceeds latency limit from edge %s: %v > %v",
					pod.Name, edgeProxy, latencyDuration, latencyLimit)
				meetsLimit = false
				break
			}
		}

		if !meetsLimit {
			log.Printf("Pod %s no longer meets latency constraint, rescheduling...", pod.Name)
			s.reschedulePod(&pod, nodeScores, clusterName, clientset)
			return
		}
	}

	currentScore := nodeScores[clusterName][currentNodeIP]
	bestIP, bestCluster, err := s.GetNodeIPForSchedule(nodeScores, &pod)
	if err != nil {
		log.Printf("Error getting best node for pod %s: %v", pod.Name, err)
		return
	}
	bestScore := nodeScores[bestCluster][bestIP]

	if bestScore > currentScore*1.2 {
		log.Printf("Pod %s is on a suboptimal node. Best score: %.2f vs current %.2f. Rescheduling...", pod.Name, bestScore, currentScore)
		s.reschedulePod(&pod, nodeScores, clusterName, clientset)
	} else {
		log.Printf("Pod %s is on an optimal node: %s", pod.Name, pod.Spec.NodeName)
	}
}

/*
Function that is performed periodically, update state and check if there is a possible better node for a pod.
TODO - pods with max_latency should be prioritize
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
				continue
			}

			for _, pod := range pods.Items {
				if _, hasLimit := pod.Annotations["proxima-scheduler/max-latency-ms"]; hasLimit {
					s.handlePodReconciliation(pod, nodeScores, edgeToLatencies, clusterName, clientset)
				}
			}

			for _, pod := range pods.Items {
				if _, hasLimit := pod.Annotations["proxima-scheduler/max-latency-ms"]; !hasLimit {
					s.handlePodReconciliation(pod, nodeScores, edgeToLatencies, clusterName, clientset)
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
	limitStr, hasLimit := pod.Annotations["proxima-scheduler/max-latency-ms"]

	var latencyLimit time.Duration
	if hasLimit {
		ms, err := strconv.ParseInt(limitStr, 10, 64)
		if err != nil {
			log.Printf("Invalid max-latency-ms annotation on pod %s: %v", pod.Name, err)
			return "", "", fmt.Errorf("invalid max-latency-ms: %w", err)
		}
		latencyLimit = time.Duration(ms) * time.Millisecond
	}

	edgeToLatencies := s.obtainEdgeNodeLatencies()

	podsPerNode := make(map[string]bool)

	appLabel := pod.Labels["app"]
	if appLabel != "" {
		for clusterName, clientset := range s.Clientsets {
			for _, namespace := range s.IncludedNamespaces {
				pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
					LabelSelector: fmt.Sprintf("app=%s", appLabel),
				})
				if err != nil {
					log.Printf("Error listing pods for app=%s in cluster=%s: %v", appLabel, clusterName, err)
					continue
				}

				for _, existingPod := range pods.Items {
					if existingPod.Spec.NodeName == "" || existingPod.DeletionTimestamp != nil {
						continue
					}

					node, err := clientset.CoreV1().Nodes().Get(context.TODO(), existingPod.Spec.NodeName, metav1.GetOptions{})
					if err != nil {
						log.Printf("Error getting node for pod %s: %v", existingPod.Name, err)
						continue
					}

					nodeIP, err := util.GetNodeInternalIP(node)
					if err != nil {
						log.Printf("Error getting node IP for pod %s: %v", existingPod.Name, err)
						continue
					}

					podsPerNode[nodeIP] = true
				}
			}
		}
	}

	// Based on score only
	// Any other metrics?
	bestFreeNode := ""
	bestScore := -math.MaxFloat64
	epsilon := 0.1

	for clusterName, nodes := range nodeScores {
		for nodeIP, score := range nodes {
			//if podsPerNode[nodeIP] {
			//	continue
			//}

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
			}
		}
	}

	if bestFreeNode == "" {
		return "", "", fmt.Errorf("no suitable node found for scheduling pod %s", pod.Name)
	}

	cands := make([]NodeCandidate, 0, 8)
	threshold := bestScore * (1.0 - epsilon)

	for clusterName, nodes := range nodeScores {
		for nodeIP, score := range nodes {
			if score < threshold {
				continue
			}

			//if podsPerNode[nodeIP]
			if !hasEnoughCapacity(s.Clientsets[clusterName], nodeIP, pod) {
				continue
			}

			if hasLimit {
				meets := true
				for _, lat := range edgeToLatencies {
					v, ok := lat[nodeIP]
					if !ok {
						meets = false
						break
					}
					if time.Duration(v*float64(time.Millisecond)) > latencyLimit {
						meets = false
						break
					}
				}
				if !meets {
					continue
				}
			}

			cands = append(cands, NodeCandidate{ip: nodeIP, cluster: clusterName, score: score})
		}
	}

	if len(cands) == 0 {
		return "", "", fmt.Errorf("no near-optimal node found for scheduling pod %s", pod.Name)
	}

	pickedIP, pickedCluster := pickWeightedByScore(cands)
	return pickedIP, pickedCluster, nil
}

func pickWeightedByScore(cands []NodeCandidate) (string, string) {
	min := math.MaxFloat64
	for _, c := range cands {
		if c.score < min {
			min = c.score
		}
	}
	total := 0.0
	weights := make([]float64, len(cands))
	for i, c := range cands {
		w := (c.score - min) + 1e-6
		weights[i] = w
		total += w
	}
	r := rand.Float64() * total
	acc := 0.0
	for i, w := range weights {
		acc += w
		if r <= acc {
			return cands[i].ip, cands[i].cluster
		}
	}
	last := cands[len(cands)-1]
	return last.ip, last.cluster
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
