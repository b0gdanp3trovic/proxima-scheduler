//WIP
//package scheduler
//
//import (
//	v1 "k8s.io/api/core/v1"
//	"k8s.io/apimachinery/pkg/api/resource"
//	"k8s.io/client-go/kubernetes"
//)
//
//func selectNodeBasedOnCapacity(nodes *v1.NodeList) {
//	for _, node := range nodes.Items {
//
//	}
//}
//
//func hasEnoughCapacity(clientset *kubernetes.Clientset, node *v1.Node, pod *v1.Pod) bool {
//	nodeResources := node.Status.Allocatable
//
//	podCPURequest := resource.NewQuantity(0, resource.DecimalSI)
//	podMemoryRequest := resource.NewQuantity(0, resource.BinarySI)
//}