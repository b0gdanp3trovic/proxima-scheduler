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
	Clientset          *kubernetes.Clientset
	SchedulerName      string
	IncludedNamespaces []string
	StopCh             chan struct{}
	DB                 util.Database
}

func NewScheduler(schedulerName string, includedNamespaces []string, clientset *kubernetes.Clientset, db util.Database) (*Scheduler, error) {
	s := &Scheduler{
		Clientset:          clientset,
		IncludedNamespaces: includedNamespaces,
		SchedulerName:      schedulerName,
		StopCh:             make(chan struct{}),
		DB:                 db,
	}

	return s, nil
}

func (s *Scheduler) Run() {
	for _, ns := range s.IncludedNamespaces {
		go func(namespace string) {
			podListWatcher := cache.NewListWatchFromClient(
				s.Clientset.CoreV1().RESTClient(),
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
							s.schedulePod(pod)
						}
					},
				},
			)

			// Start the controller in the goroutine
			controller.Run(s.StopCh)
		}(ns)
	}
}

func (s *Scheduler) schedulePod(pod *v1.Pod) {
	nodes, err := util.DiscoverNodes(s.Clientset)

	if err != nil {
		fmt.Printf("Error listing nodes: %v\n", err)
		return
	}

	if len(nodes.Items) == 0 {
		fmt.Println("No nodes available")
		return
	}

	// TODO: enable config option for the logic behind selecting nodes
	// selectedNode := selectNodeBasedOnCapacity(s.Clientset, nodes, pod)
	// selectedNode := selectNodeBasedOnScore(s.Clientset, nodes, pod, s.DB)
	selectedNode, err := selectNodeBasedOnScore(s.Clientset, nodes, pod, s.DB)

	if err != nil {
		fmt.Printf("Error selecting node for pod %s: %v\n", pod.GetName(), err)
	}

	if selectedNode != nil {
		fmt.Printf("Scheduling pod %s to node %s\n", pod.GetName(), *selectedNode)
		// Bind the pod to the selected node
		s.bindPodToNode(s.Clientset, pod, selectedNode)
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
