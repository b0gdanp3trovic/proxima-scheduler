package scheduler

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	appsv1 "k8s.io/api/apps/v1"
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

type nodeScorePair struct {
	Name  string
	Score float64
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

// Apply everywhere except source
func (s *Scheduler) ApplyDeployment(deployment *appsv1.Deployment, sourceCluster string) {
	for clusterName, clientset := range s.Clientsets {
		if clusterName == sourceCluster {
			continue
		}

		deployCopy := deployment.DeepCopy()
		replicas := int32(0)
		deployCopy.Spec.Replicas = &replicas
		deployCopy.ResourceVersion = ""
		deployCopy.UID = ""

		_, err := clientset.AppsV1().Deployments(deployCopy.Namespace).Create(context.TODO(), deployCopy, metav1.CreateOptions{})
		if err != nil {
			_, updateErr := clientset.AppsV1().Deployments(deployCopy.Namespace).Update(context.TODO(), deployCopy, metav1.UpdateOptions{})
			if updateErr != nil {
				log.Printf("Error applying deployment to cluster %s: %v", clusterName, updateErr)
				continue
			}
		}

		log.Printf("Deployment applied to cluster %s", clusterName)
	}
}

// For now accept only deployments, handy since you can set desired number
func getDeploymentFromPod(clientset *kubernetes.Clientset, pod *v1.Pod) (*appsv1.Deployment, error) {
	rsOwner := metav1.GetControllerOf(pod)
	if rsOwner == nil || rsOwner.Kind != "ReplicaSet" {
		return nil, fmt.Errorf("pod %s has no ReplicaSet owner", pod.Name)
	}

	rs, err := clientset.AppsV1().ReplicaSets(pod.Namespace).Get(context.TODO(), rsOwner.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get ReplicaSet: %w", err)
	}

	deployOwner := metav1.GetControllerOf(rs)
	if deployOwner == nil || deployOwner.Kind != "Deployment" {
		return nil, fmt.Errorf("replicaSet %s has no Deployment owner", rs.Name)
	}

	deployment, err := clientset.AppsV1().Deployments(pod.Namespace).Get(context.TODO(), deployOwner.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Deployment: %w", err)
	}

	return deployment, nil
}

func getSortedNodeNamesByScore(clientset *kubernetes.Clientset, nodeScores map[string]float64) []string {
	var pairs []nodeScorePair

	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Printf("Failed to list nodes: %v", err)
		return nil
	}

	ipToName := map[string]string{}
	for _, node := range nodes.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == v1.NodeInternalIP {
				ipToName[addr.Address] = node.Name
			}
		}
	}

	for ip, score := range nodeScores {
		if name, exists := ipToName[ip]; exists {
			pairs = append(pairs, nodeScorePair{name, score})
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].Score > pairs[j].Score
	})

	var sortedNodeNames []string
	for _, pair := range pairs {
		sortedNodeNames = append(sortedNodeNames, pair.Name)
	}
	return sortedNodeNames
}

func setAffinityPreferences(deployment *appsv1.Deployment, sortedNodeNames []string) {
	preferredTerms := []v1.PreferredSchedulingTerm{}
	weight := int32(100)

	for _, nodeName := range sortedNodeNames {
		preferredTerms = append(preferredTerms, v1.PreferredSchedulingTerm{
			Weight: weight,
			Preference: v1.NodeSelectorTerm{
				MatchExpressions: []v1.NodeSelectorRequirement{
					{
						Key:      "kubernetes.io/hostname",
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{nodeName},
					},
				},
			},
		})
		weight -= 10
		if weight <= 0 {
			weight = 1
		}
	}

	deployment.Spec.Template.Spec.Affinity = &v1.Affinity{
		NodeAffinity: &v1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: preferredTerms,
		},
	}
}
