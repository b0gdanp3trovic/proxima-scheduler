package pinger

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	bing "github.com/prometheus-community/pro-bing"
	"k8s.io/client-go/kubernetes"
)

type AggregatedLatencyKey struct {
	Source      string
	Destination string
}

type Pinger struct {
	Addr                map[string]struct{}
	Latencies           map[string]time.Duration
	AggregatedLatencies map[AggregatedLatencyKey]time.Duration
	Interval            time.Duration
	stopChan            chan struct{}
	DBEnabled           bool
	DB                  util.Database
	Clientset           *kubernetes.Clientset
	mu                  sync.Mutex
	NodeIP              string
	ExternalNodeIP      string
	EdgeProxies         []string
}

func NewPinger(
	interval time.Duration,
	clientset *kubernetes.Clientset,
	dbEnabled bool,
	db util.Database,
	nodeIP string,
	edgeProxies []string,
) (*Pinger, error) {
	p := &Pinger{
		Addr:                make(map[string]struct{}),
		Latencies:           make(map[string]time.Duration),
		AggregatedLatencies: make(map[AggregatedLatencyKey]time.Duration),
		Interval:            interval,
		stopChan:            make(chan struct{}),
		DBEnabled:           dbEnabled,
		DB:                  db,
		Clientset:           clientset,
		NodeIP:              nodeIP,
		ExternalNodeIP:      "",
		EdgeProxies:         []string{},
	}

	// Filter out current node ip from edge proxy IPs
	externalNodeIP, filteredEdgeProxies, err := util.ObtainEdgeProxies(edgeProxies, p.Clientset, p.NodeIP)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain edge proxies: %w", err)
	}

	p.EdgeProxies = filteredEdgeProxies
	p.ExternalNodeIP = externalNodeIP

	return p, nil
}

func (p *Pinger) AddAddress(address string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.Addr[address] = struct{}{}
	p.Latencies[address] = 0
}

func (p *Pinger) RemoveAddress(address string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.Addr, address)
	delete(p.Latencies, address)
}

func (p *Pinger) updateAddresses() {
	nodes, err := util.DiscoverNodes(p.Clientset)
	if err != nil {
		fmt.Printf("Error discovering nodes: %v\n", err)
		return
	}

	currentNodes := make(map[string]struct{})
	for _, node := range nodes.Items {
		var address string
		// Get the Internal IP of the node
		for _, addr := range node.Status.Addresses {
			if addr.Type == "InternalIP" {
				address = addr.Address
				break
			}
		}

		if address == "" {
			fmt.Printf("No Internal IP found for node %s\n", node.Name)
			continue
		}

		/*
		   The node hosting the pinger is designated as an "edge" node.
		   We always ensure to skip these nodes, as we want to ping only
		   core or non-edge nodes.
		*/
		if address == p.NodeIP {
			fmt.Printf("Skipping node %s, as it is detected as a host node.\n", p.NodeIP)
			continue
		}

		currentNodes[address] = struct{}{}

		if _, exists := p.Addr[address]; !exists {
			p.AddAddress(address)
			fmt.Printf("Discovered and added node: %s\n", address)
		}
	}

	for address := range p.Addr {
		if _, exists := currentNodes[address]; !exists {
			p.RemoveAddress(address)
			fmt.Printf("Node removed: %s\n", address)
		}

		p.mu.Lock()
		for _, ep := range p.EdgeProxies {
			key := AggregatedLatencyKey{Source: ep, Destination: address}
			delete(p.AggregatedLatencies, key)
		}
		p.mu.Unlock()
	}
}

func (p *Pinger) PingAll() {
	var addresses []string

	p.mu.Lock()
	// Add worker node addresses
	for address := range p.Addr {
		addresses = append(addresses, address)
	}

	// Also add edge proxies
	fmt.Println("Edge proxy addresses:")
	for _, edgeProxyAddress := range p.EdgeProxies {
		fmt.Printf(" - %s\n", edgeProxyAddress)
		addresses = append(addresses, edgeProxyAddress)
	}

	p.mu.Unlock()

	for _, address := range addresses {
		// Create a new pinger instance
		pinger, err := bing.NewPinger(address)

		// Windows support
		pinger.SetPrivileged(true)

		if err != nil {
			fmt.Printf("Failed to create pinger for %s: %v\n", address, err)
			continue
		}

		pinger.Count = 1
		pinger.Timeout = 2 * time.Second

		err = pinger.Run()
		if err != nil {
			fmt.Printf("Failed to ping %s: %v\n", address, err)
			continue
		}

		stats := pinger.Statistics()

		p.mu.Lock()
		if stats.PacketsRecv > 0 {
			p.Latencies[address] = stats.AvgRtt
			fmt.Printf("Ping to %s: %v\n", address, stats.AvgRtt)
		} else {
			fmt.Printf("Ping to %s failed: No packets received\n", address)
		}
		p.mu.Unlock()
	}

	// Save to DB if enabled
	if p.DBEnabled {
		p.SaveLatenciesToDB()
	}
}

func (p *Pinger) AggregateLatencies() {
	for _, ep := range p.EdgeProxies {
		latencyToCurrent, err := p.DB.GetLatency(ep, p.ExternalNodeIP)

		if err != nil {
			fmt.Printf("Failed to get latency from %s to %s: %v\n", ep, p.ExternalNodeIP, err)
			continue
		}

		for address, latency := range p.Latencies {
			// Don't add ep1 -> ep2 -> ep1
			if address != ep {
				totalLatency := latencyToCurrent + latency
				key := AggregatedLatencyKey{Source: ep, Destination: address}
				p.AggregatedLatencies[key] = totalLatency
			}
		}
	}

	fmt.Println("Aggregated Latencies:")
	for key, latency := range p.AggregatedLatencies {
		fmt.Printf("Source: %s -> Destination: %s, Latency: %v\n", key.Source, key.Destination, latency)
	}
}

func (p *Pinger) SaveLatenciesToDB() {
	if !p.DBEnabled {
		log.Println("Database disabled, skipping database save.")
		return
	}

	if len(p.Latencies) == 0 {
		log.Println("Empty latencies table, nothing to save.")
		return
	}

	log.Printf("Saving latencies to DB: %+v", p.Latencies)

	if err := p.DB.SavePingTime(p.Latencies, p.ExternalNodeIP); err != nil {
		log.Printf("Failed to save latencies to the database: %v", err)
	} else {
		fmt.Println("Successfully saved latencies to the database.")
	}
}

/*
TODO
func(p *Pinger) SaveAggregatedLatenciesToDB() {
	...
}
*/

func (p *Pinger) Run() {
	go func() {
		p.updateAddresses()
		fmt.Println("Initial node discovery completed.")

		ticker := time.NewTicker(p.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				p.updateAddresses()
				fmt.Println("Finished updating addresses.")
				p.PingAll()
				fmt.Println("Finished pinging all addresses.")
				p.AggregateLatencies()
				fmt.Println("Finished aggregating latencies.")

			case <-p.stopChan:
				fmt.Println("Stopping pinger.")
				// Save any remaining latencies before stopping
				if p.DBEnabled {
					p.SaveLatenciesToDB()
				}
				return
			}
		}
	}()
}

func (p *Pinger) Stop() {
	close(p.stopChan)
}
