package util

import (
	"context"
	"fmt"
	"log"
	"path/filepath"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func GetClientset() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err == nil {
		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create clientset using in-cluster config: %w", err)
		}
		fmt.Println("Detected running inside the Kubernetes cluster")
		return clientset, nil
	}

	fmt.Println("Detected running outside the Kubernetes cluster, trying to load kubeconfig")
	home := HomeDir()
	if home == "" {
		return nil, fmt.Errorf("home directory not found")
	}

	kubeconfig := filepath.Join(home, ".kube", "config")
	config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset using kubeconfig: %w", err)
	}

	fmt.Println("Connection to cluster successfully configured using kubeconfig")
	return clientset, nil
}

func DiscoverNodes(clientset *kubernetes.Clientset) (*v1.NodeList, error) {
	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Fatalf("Failed to list nodes: %v", err)
		return nil, err
	}

	return nodes, nil
}

// Primarily used to provide formatted data to a Pinger instance
func ExtractNodeAddresses(nodes []v1.Node) []string {
	var addresses []string
	for _, node := range nodes {
		for _, addr := range node.Status.Addresses {
			if addr.Type == v1.NodeInternalIP {
				addresses = append(addresses, addr.Address+":80")
			}
		}
	}
	return addresses
}
