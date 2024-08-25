package scheduler

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

type Scheduler struct {
	Clientset          *kubernetes.Clientset
	IncludedNamespaces []string
}

func NewScheduler(includedNamespaces []string) (*Scheduler, error) {
	s := &Scheduler{
		IncludedNamespaces: includedNamespaces,
	}

	clientset, err := s.configure()
	if err != nil {
		return nil, err
	}

	s.Clientset = clientset
	return s, nil
}

func (s *Scheduler) Run() {
	podListWatcher := cache.NewListWatchFromClient(
		s.Clientset.CoreV1().RESTClient(),
		"pods",
		v1.NamespaceAll,
		fields.Everything(),
	)

	_, controller := cache.NewInformer(
		podListWatcher,
		&v1.Pod{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				pod := obj.(*v1.Pod)
				if pod.Spec.SchedulerName == "proxima-scheduler" && pod.Spec.NodeName == "" {
					s.schedulePod(s.Clientset, pod)
				}
			},
		},
	)

	stop := make(chan struct{})
	defer close(stop)

	go controller.Run(stop)

	select {}
}

func (s *Scheduler) configure() (*kubernetes.Clientset, error) {
	var kubeconfig *string
	if home := util.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}

	flag.Parse()

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	fmt.Println("Connection to cluster successfully configured.")
	return clientset, nil
}

func (s *Scheduler) schedulePod(clientset *kubernetes.Clientset, pod *v1.Pod) {
	nodes, err := util.DiscoverNodes(clientset)

	if err != nil {
		fmt.Printf("Error listing nodes: %v\n", err)
		return
	}

	if len(nodes.Items) == 0 {
		fmt.Println("No nodes available")
		return
	}

	selectedNode := selectNodeBasedOnCapacity(clientset, nodes, pod)
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

	fmt.Printf("Successfully scheduled pod %s to node %s.", pod.GetName(), *nodeName)
}
