package main

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
}

func NewNodeRegister(interval time.Duration, clientset *kubernetes.Clientset) *NodeRegister {
	return &NodeRegister{
		interval:  interval,
		clientset: clientset,
	}
}

func (nr *NodeRegister) Start() {
	for {
		nodes, err := util.DiscoverNodes(nr.clientset)
		if err != nil {
			log.Printf("Failed to list nodes: %v", err)
			time.Sleep(10 * time.Second)
			continue
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

			err := nr.registerNodeWithConsul(service)
			if err != nil {
				log.Printf("Failed to register node %s with Consul: %v", node.Name, err)
			} else {
				log.Printf("Registered node %s (%s) with Consul", node.Name, nodeIP)
			}
		}

		time.Sleep(30 * time.Second)
	}
}

func (nr *NodeRegister) registerNodeWithConsul(service ConsulService) error {
	data, err := json.Marshal(service)
	if err != nil {
		return fmt.Errorf("failed to marshal service: %v", err)
	}

	resp, err := http.Post(nr.consulURL, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to send registration request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected response code: %d", resp.StatusCode)
	}

	return nil
}
