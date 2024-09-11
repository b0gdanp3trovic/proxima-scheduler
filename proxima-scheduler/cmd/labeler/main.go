package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
)

type CloudProvider struct {
	Name        string
	MetadataURL string
}

var cloudProviders = []CloudProvider{
	{Name: "gcp", MetadataURL: "http://metadata.google.internal/computeMetadata/v1/instance/zone"},
	{Name: "aws", MetadataURL: "http://169.254.169.254/latest/meta-data/placement/availability-zone"},
	{Name: "hetzner", MetadataURL: "http://169.254.169.254/hetzner/v1/metadata"},
}

func getMetadata(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func detectCloudProvider() (CloudProvider, error) {
	for _, provider := range cloudProviders {
		if _, err := getMetadata(provider.MetadataURL); err == nil {
			return provider, nil
		}
	}
	return CloudProvider{}, fmt.Errorf("unknown cloud provider")
}

func labelNode(clientset *kubernetes.Clientset, nodeName, labelKey, labelValue string) error {
	ctx := context.Background()

	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	node.Labels[labelKey] = labelValue

	_, err = clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	fmt.Printf("Node %s labeled with %s=%s\n", nodeName, labelKey, labelValue)
	return nil
}

func main() {
	clientset, err := util.GetClientset()
	if err != nil {
		log.Fatalf("Failed to obtain clientset: %v", err)
		os.Exit(1)
	}

	provider, err := detectCloudProvider()
	if err != nil {
		fmt.Println("Error detecting cloud provider:", err)
		os.Exit(1)
	}

	metadata, err := getMetadata(provider.MetadataURL)
	if err != nil {
		fmt.Println("Error fetching metadata:", err)
		os.Exit(1)
	}

	zone := metadata
	if provider.Name == "hetzner" {
		zone = extractHetznerZone(metadata)
	}

	nodeName, _ := os.Hostname()
	err = labelNode(clientset, nodeName, "proxima-availability-zone", zone)
	if err != nil {
		fmt.Println("Error labeling node:", err)
		os.Exit(1)
	}
}

func extractHetznerZone(metadata string) string {
	// Assuming Hetzner metadata provides a field like 'availability-zone'
	lines := strings.Split(metadata, "\n")
	for _, line := range lines {
		if strings.Contains(line, "availability-zone") {
			return strings.Split(line, ":")[1]
		}
	}
	return "unknown"
}
