package noderegister

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type ConsulService struct {
	ID         string            `json:"ID"`
	Name       string            `json:"Name"`
	Address    string            `json:"Address"`
	Tags       []string          `json:"Tags"`
	Meta       map[string]string `json:"Meta"`
	Datacenter string            `json:"Datacenter"`
}

type NodeRegister struct {
	interval    time.Duration
	clientset   *kubernetes.Clientset
	consulURL   string
	clusterName string
	httpClient  *http.Client
}

func NewNodeRegister(interval time.Duration, clientset *kubernetes.Clientset, consulURL string, clusterName string, caCertPath string) (*NodeRegister, error) {
	httpClient, err := util.NewHTTPCLient(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("Failed reading caCertPath: %v", err)
	}

	return &NodeRegister{
		interval:    interval,
		clientset:   clientset,
		consulURL:   consulURL,
		clusterName: clusterName,
		httpClient:  httpClient,
	}, nil
}

func (nr *NodeRegister) registerNodesToConsul() error {
	nodes, err := util.DiscoverNodes(nr.clientset)
	if err != nil {
		return fmt.Errorf("failed to list nodes: %v", err)
	}

	for _, node := range nodes.Items {
		var nodeIP string
		for _, addr := range node.Status.Addresses {
			if addr.Type == v1.NodeInternalIP {
				nodeIP = addr.Address
				break
			}
		}

		if nodeIP == "" {
			log.Printf("No Internal IP found for node: %s", node.Name)
			continue
		}

		service := ConsulService{
			ID:         fmt.Sprintf("node-%s", node.Name),
			Name:       "k8s-node",
			Address:    nodeIP,
			Tags:       []string{"kubernetes-node", fmt.Sprintf("cluster:%s", nr.clusterName)},
			Meta:       map[string]string{"nodeName": node.Name},
			Datacenter: nr.clusterName,
		}

		err := nr.sendRegistrationRequest(service)
		if err != nil {
			log.Printf("Failed to register node %s with Consul: %v", node.Name, err)
		} else {
			log.Printf("Registered node %s (%s) with Consul", node.Name, nodeIP)
		}
	}

	return nil
}

func (nr *NodeRegister) sendRegistrationRequest(service ConsulService) error {
	url := fmt.Sprintf("%s/v1/agent/service/register", nr.consulURL)

	data, err := json.Marshal(service)
	if err != nil {
		return fmt.Errorf("failed to marshal service: %v", err)
	}

	resp, err := nr.httpClient.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to send registration request: %v", err)
	}
	defer resp.Body.Close()

	responseBody := new(bytes.Buffer)
	if err != nil {
		return fmt.Errorf("failed to read response body: %v", err)
	}

	_, err = responseBody.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected response code: %d, response body: %s", resp.StatusCode, responseBody.String())
	}

	return nil
}

func (nr *NodeRegister) Run() {
	log.Printf("Starting node registrator...")

	go func() {
		ticker := time.NewTicker(nr.interval)
		defer ticker.Stop()

		for range ticker.C {
			log.Println("Registering nodes...")
			nr.registerNodesToConsul()
		}
	}()
}
