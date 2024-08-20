package scheduler

import (
	"fmt"

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
