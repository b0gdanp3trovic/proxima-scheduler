package scheduler

import (
	"context"
	"fmt"

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
}

func NewScheduler(schedulerName string, includedNamespaces []string, inClusterClientset *kubernetes.Clientset, kubeconfigs map[string]string, db util.Database) (*Scheduler, error) {
	clientsets := make(map[string]*kubernetes.Clientset)

	for clusterName, kubeconfigPath := range kubeconfigs {
		clientset, err := util.GetClientsetForCluster(kubeconfigPath)
		if err != nil {
			fmt.Printf("Failed to load cluster %s: %v\n", clusterName, err)
			continue
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
	}, nil
}

func (s *Scheduler) Run() {
	for clusterName, clientset := range s.Clientsets {
		for _, ns := range s.IncludedNamespaces {
			go func(clusterName, namespace string, clientset *kubernetes.Clientset) {
				fmt.Printf("Watching pods in namespace %s on cluster %s\n", namespace, clusterName)

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
								fmt.Printf("New pod detected in cluster %s, scheduling...\n", clusterName)
								s.schedulePod(pod, clientset)
							}
						},
					},
				)

				controller.Run(s.StopCh)
			}(clusterName, ns, clientset)
		}
	}
}

func (s *Scheduler) schedulePod(pod *v1.Pod, clientset *kubernetes.Clientset) {
	nodes, err := util.DiscoverNodes(clientset)

	if err != nil {
		fmt.Printf("Error listing nodes: %v\n", err)
		return
	}

	if len(nodes.Items) == 0 {
		fmt.Println("No nodes available")
		return
	}

	// TODO: enable config option for the logic behind selecting nodes
	// selectedNode := selectNodeBasedOnCapacity(clientset, nodes, pod)
	// selectedNode := selectNodeBasedOnScore(clientset, nodes, pod, s.DB)
	selectedNode, err := selectNodeBasedOnScore(clientset, nodes, pod, s.DB)

	if err != nil {
		fmt.Printf("Error selecting node for pod %s: %v\n", pod.GetName(), err)
	}

	if selectedNode != nil {
		fmt.Printf("Scheduling pod %s to node %s\n", pod.GetName(), *selectedNode)
		// Bind the pod to the selected node
		s.bindPodToNode(clientset, pod, selectedNode)
	}
}

func (s *Scheduler) bindPodToNode(clientset *kubernetes.Clientset, pod *v1.Pod, nodeName *string) {
	binding := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: pod.Namespace,
			Name:      pod.Name,
			UID:       pod.UID,
		},
		Target: v1.ObjectReference{
			Kind: "Node",
			Name: *nodeName,
		},
	}

	err := clientset.CoreV1().Pods(pod.Namespace).Bind(context.TODO(), binding, metav1.CreateOptions{})
	if err != nil {
		fmt.Printf("Error binding pod: %v\n", err)
		return
	}

	fmt.Printf("Successfully scheduled pod %s to node %s.\n", pod.GetName(), *nodeName)
}
