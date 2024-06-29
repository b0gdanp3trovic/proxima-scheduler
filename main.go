package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	var kubeconfig *string
	if home := homeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}

	flag.Parse()

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	podListWatcher := cache.NewListWatchFromClient(
		clientset.CoreV1().RESTClient(),
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
					schedulePod(clientset, pod)
				}
			},
		},
	)

	stop := make(chan struct{})
	defer close(stop)

	go controller.Run(stop)

	select {}
}

func schedulePod(clientset *kubernetes.Clientset, pod *v1.Pod) {
	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})

	if err != nil {
		fmt.Printf("Error listing nodes: %v\n", err)
		return
	}

	if len(nodes.Items) == 0 {
		fmt.Println("No nodes available")
		return
	}

	// TODO: implement
	selectedNode := nodes.Items[0].Name
	fmt.Printf("Scheduling pod %s to node %s\n", pod.GetName(), selectedNode)

	patch := []byte(fmt.Sprintf(`{"spec": {"nodeName": "%s"}}`, selectedNode))
	_, err = clientset.CoreV1().Pods(pod.GetNamespace()).Patch(context.TODO(), pod.GetName(), types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		fmt.Printf("Error updating pod: %v\n", err)
	}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE")
}
