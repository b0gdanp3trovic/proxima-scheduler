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

func DiscoverEdgeNodesByDaemonset(clientset *kubernetes.Clientset, namespace string, daemonsetName string) (map[string]string, error) {
	edges := make(map[string]string)

	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", daemonsetName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods for DaemonSet %s in namespace %s: %v", daemonsetName, namespace, err)
	}

	for _, pod := range pods.Items {
		nodeName := pod.Spec.NodeName
		if _, exists := edges[nodeName]; exists {
			continue
		}

		node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		if err != nil {
			log.Printf("Failed to get node %s: %v", nodeName, err)
			continue
		}

		for _, addr := range node.Status.Addresses {
			if addr.Type == v1.NodeInternalIP {
				edges[nodeName] = addr.Address
				log.Printf("Discovered edge node %s with internal IP %s", nodeName, addr.Address)
				break
			}
		}
	}

	fmt.Printf("Discovered %d edge nodes based on DaemonSet %s\n", len(edges), daemonsetName)
	return edges, nil
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

func GetNodeExternalIP(clientset *kubernetes.Clientset, internalNodeIP string) (string, error) {
	nodes, err := DiscoverNodes(clientset)
	if err != nil {
		return "", fmt.Errorf("failed to discover nodes: %w", err)
	}

	for _, node := range nodes.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == "InternalIP" && addr.Address == internalNodeIP {
				for _, extAddr := range node.Status.Addresses {
					if extAddr.Type == "ExternalIP" {
						return extAddr.Address, nil
					}
				}
				return "", fmt.Errorf("node %s has no ExternalIP", node.Name)
			}
		}
	}

	return "", fmt.Errorf("node with InternalIP %s not found", internalNodeIP)
}
