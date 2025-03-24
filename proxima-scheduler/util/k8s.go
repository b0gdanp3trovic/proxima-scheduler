package util

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func GetInClusterClientset() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err == nil {
		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create clientset using in-cluster config: %w", err)
		}
		log.Println("Detected running inside the Kubernetes cluster")
		return clientset, nil
	}

	log.Println("Detected running outside the Kubernetes cluster, trying to load kubeconfig")
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

	log.Println("Connection to cluster successfully configured using kubeconfig")
	return clientset, nil
}

func DiscoverNodes(clientset *kubernetes.Clientset, edgeProxies []string) (*v1.NodeList, error) {
	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Fatalf("Failed to list nodes: %v", err)
		return nil, err
	}

	if len(edgeProxies) == 0 {
		return nodes, nil
	}

	edgeProxyMap := make(map[string]struct{})
	for _, ip := range edgeProxies {
		edgeProxyMap[ip] = struct{}{}
	}

	var filteredItems []v1.Node
	for _, node := range nodes.Items {
		internalIP, err := getNodeInternalIP(node)
		if err != nil {
			return nil, fmt.Errorf("Failed obtaining internal ip: %v", err)
		}

		externalIp, err := getNodeExternalIP(clientset, internalIP)
		if err != nil {
			return nil, fmt.Errorf("Failed obtaining external ip: %v", err)
		}

		if _, exists := edgeProxyMap[externalIp]; exists {
			continue
		}

		filteredItems = append(filteredItems, node)
	}

	filteredNodes := &v1.NodeList{
		Items: filteredItems,
	}

	return filteredNodes, nil
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

	log.Printf("Discovered %d edge nodes based on DaemonSet %s\n", len(edges), daemonsetName)
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

func getNodeExternalIP(clientset *kubernetes.Clientset, internalNodeIP string) (string, error) {
	nodes, err := DiscoverNodes(clientset, nil)
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

func getNodeInternalIP(node v1.Node) (string, error) {
	for _, addr := range node.Status.Addresses {
		if addr.Type == v1.NodeInternalIP {
			return addr.Address, nil
		}
	}
	return "", fmt.Errorf("Failed obtaining node internal IP.")
}

func ObtainEdgeProxies(unfilteredEdgeproxies []string, clientset *kubernetes.Clientset, nodeIP string) (string, []string, error) {
	var filteredEdgeProxies []string
	currentNodeIP, err := getNodeExternalIP(clientset, nodeIP)
	if err != nil {
		log.Printf("Error obtaining current node IP: %v\n", err)
		return "", nil, err
	}

	for _, edgeProxyIP := range unfilteredEdgeproxies {
		if edgeProxyIP != currentNodeIP {
			filteredEdgeProxies = append(filteredEdgeProxies, edgeProxyIP)
		}
	}
	return currentNodeIP, filteredEdgeProxies, nil
}

func IsEdgeProxy(address string, edgeProxies []string) bool {
	for _, ep := range edgeProxies {
		if ep == address {
			return true
		}
	}
	return false
}

func LoadKubeconfigs(dir string) (map[string]string, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	kubeconfigs := make(map[string]string)
	for _, file := range files {
		if file.IsDir() || file.Name() == "..data" {
			continue
		}

		kubeconfigPath := filepath.Join(dir, file.Name())

		fileName := file.Name()
		ext := filepath.Ext(fileName)
		nameWithoutExt := strings.TrimSuffix(fileName, ext)

		kubeconfigs[nameWithoutExt] = kubeconfigPath
	}

	return kubeconfigs, nil
}

func GetClientsetForCluster(kubeconfigPath string) (*kubernetes.Clientset, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig %s: %w", kubeconfigPath, err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	return clientset, nil
}

func GetNodeByInternalIP(clientset *kubernetes.Clientset, internalIP string) (*v1.Node, error) {
	nodes, err := DiscoverNodes(clientset, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to discover nodes: %w", err)
	}

	for _, node := range nodes.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == v1.NodeInternalIP && addr.Address == internalIP {
				return &node, nil
			}
		}
	}

	return nil, fmt.Errorf("node with internal IP %s not found", internalIP)
}
