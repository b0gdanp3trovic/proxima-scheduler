package scheduler

import (
	"fmt"
	"log"
	"math"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"
)

func selectNodeBasedOnCapacity(clientset *kubernetes.Clientset, nodes *v1.NodeList, pod *v1.Pod) *string {
	for _, node := range nodes.Items {
		nodeAddress := node.Status.Addresses[0].Address
		if hasEnoughCapacity(clientset, nodeAddress, pod) {
			selectedNode := node.Name
			log.Printf("Selected node %s\n", selectedNode)
			// Bind the pod to the selected node
			return &selectedNode
		}
	}

	log.Println("No suitable nodes available")
	return nil
}

func selectNodeBasedOnLatency(clientset *kubernetes.Clientset, nodes *v1.NodeList, pod *v1.Pod, db util.Database) (*string, error) {
	nodeLatencies, err := db.GetAveragePingTime()
	if err != nil {
		return nil, fmt.Errorf("failed to get average ping times: %v", err)
	}

	var selectedNode *string
	lowestLatency := math.MaxFloat64

	for _, node := range nodes.Items {
		nodeAddress := node.Status.Addresses[0].Address

		latency, exists := nodeLatencies[nodeAddress]
		if !exists {
			log.Printf("Node %s does not have latency data, proceeding...\n", nodeAddress)
			continue
		}

		if !hasEnoughCapacity(clientset, nodeAddress, pod) {
			log.Printf("Node %s does not have enough capacity, proceeding...\n", nodeAddress)
			continue
		}

		if latency < lowestLatency {
			lowestLatency = latency
			selectedNode = &node.Name
		}
	}

	if selectedNode == nil {
		return nil, fmt.Errorf("no suitable node found for pod %s", pod.Name)
	}

	return selectedNode, nil
}

func hasEnoughCapacity(clientset *kubernetes.Clientset, nodeIP string, pod *v1.Pod) bool {
	node, err := util.GetNodeByInternalIP(clientset, nodeIP)

	if err != nil {
		log.Printf("Error checking capacity for node %s: %v", nodeIP, err)
		return false
	}

	nodeResources := node.Status.Allocatable

	podCPURequest := resource.NewQuantity(0, resource.DecimalSI)
	podMemoryRequest := resource.NewQuantity(0, resource.BinarySI)

	for _, container := range pod.Spec.Containers {
		if container.Resources.Requests != nil {
			if cpu, ok := container.Resources.Requests[v1.ResourceCPU]; ok {
				podCPURequest.Add(cpu)
			}
			if memory, ok := container.Resources.Requests[v1.ResourceMemory]; ok {
				podMemoryRequest.Add(memory)
			}
		}
	}

	if nodeResources.Cpu().Cmp(*podCPURequest) >= 0 && nodeResources.Memory().Cmp(*podMemoryRequest) >= 0 {
		return true
	}

	return false
}
