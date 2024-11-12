package scheduler

import (
	"fmt"
	"math"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"
)

func selectNodeBasedOnCapacity(clientset *kubernetes.Clientset, nodes *v1.NodeList, pod *v1.Pod) *string {
	for _, node := range nodes.Items {
		if hasEnoughCapacity(clientset, &node, pod) {
			selectedNode := node.Name
			fmt.Printf("Selected node %s\n", selectedNode)
			// Bind the pod to the selected node
			return &selectedNode
		}
	}

	fmt.Println("No suitable nodes available")
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
			fmt.Printf("Node %s does not have latency data, proceeding...\n", nodeAddress)
			continue
		}

		if !hasEnoughCapacity(clientset, &node, pod) {
			fmt.Printf("Node %s does not have enough capacity, proceeding...\n", nodeAddress)
			continue
		}

		if latency < lowestLatency {
			lowestLatency = latency
			selectedNode = &nodeAddress
		}
	}

	if selectedNode == nil {
		return nil, fmt.Errorf("no suitable node found for pod %s", pod.Name)
	}

	return selectedNode, nil
}

func selectNodeBasedOnScore(clientset *kubernetes.Clientset, nodes *v1.NodeList, pod *v1.Pod, db util.Database) (*string, error) {
	nodeScores, err := db.GetNodeScores()
	if err != nil {
		return nil, fmt.Errorf("failed to get node scores: %v", err)
	}

	var selectedNode *string
	highestScore := -math.MaxFloat64

	for _, node := range nodes.Items {
		nodeAddress := node.Status.Addresses[0].Address

		score, exists := nodeScores[nodeAddress]
		if !exists {
			fmt.Printf("Node %s does not have score data, proceeding...\n", nodeAddress)
			continue
		}

		if !hasEnoughCapacity(clientset, &node, pod) {
			fmt.Printf("Node %s does not have enough capacity, proceeding...\n", nodeAddress)
			continue
		}

		if score > highestScore {
			highestScore = score
			selectedNode = &nodeAddress
		}
	}

	if selectedNode == nil {
		return nil, fmt.Errorf("no suitable node found for pod %s", pod.Name)
	}

	fmt.Printf("Selected node %s with score %f\n", *selectedNode, highestScore)
	return selectedNode, nil
}

func hasEnoughCapacity(clientset *kubernetes.Clientset, node *v1.Node, pod *v1.Pod) bool {
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
