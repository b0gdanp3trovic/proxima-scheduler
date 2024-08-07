package scheduler

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

func SchedulePod(clientset *kubernetes.Clientset, pod *v1.Pod) {
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

	bindPodToNode(clientset, pod, selectedNode)
}

func bindPodToNode(clientset *kubernetes.Clientset, pod *v1.Pod, nodeName string) {
	binding := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: pod.Namespace,
			Name:      pod.Name,
			UID:       pod.UID,
		},
		Target: v1.ObjectReference{
			Kind: "Node",
			Name: nodeName,
		},
	}

	err := clientset.CoreV1().Pods(pod.Namespace).Bind(context.TODO(), binding, metav1.CreateOptions{})
	if err != nil {
		fmt.Printf("Error binding pod: %v\n", err)
	}
}
