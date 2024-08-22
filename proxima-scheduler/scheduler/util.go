package scheduler

import (
	"context"
	"log"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func discoverNodes(clientset *kubernetes.Clientset) (*v1.NodeList, error) {
	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Fatalf("Failed to list nodes: %v", err)
		return nil, err
	}

	return nodes, nil
}

// Primarily used to provide formatted data to a Pinger instance
func extractNodeAddresses(nodes []v1.Node, formatAddresses bool) []string {
	var addresses []string
	for _, node := range nodes {
		for _, addr := range node.Status.Addresses {
			if addr.Type == v1.NodeInternalIP {
				if formatAddresses {
					addresses = append(addresses, addr.Address+":80")
				} else {
					addresses = append(addresses, addr.Address)
				}
			}
		}
	}
	return addresses
}
