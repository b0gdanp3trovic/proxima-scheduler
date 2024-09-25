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

type Pinger struct {
	Addr      map[string]struct{}
	Latencies map[string]time.Duration
	Interval  time.Duration
	stopChan  chan struct{}
	DBEnabled bool
	DB        Database
	Clientset *kubernetes.Clientset
	mu        sync.Mutex
	NodeIP    string
}

func NewPinger(interval time.Duration, clientset *kubernetes.Clientset, dbEnabled bool, db Database, nodeIP string) (*Pinger, error) {
	p := &Pinger{
		Addr:      make(map[string]struct{}),
		Latencies: make(map[string]time.Duration),
		Interval:  interval,
		stopChan:  make(chan struct{}),
		DBEnabled: dbEnabled,
		DB:        db,
		Clientset: clientset,
		NodeIP:    nodeIP,
	}

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
		address := node.Status.Addresses[0].Address

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
	}
}

func (p *Pinger) PingAll() {
	var addresses []string

	p.mu.Lock()
	for address := range p.Addr {
		addresses = append(addresses, address)
	}
	p.mu.Unlock()

	for _, address := range addresses {
		// Bing pinger instance
		pinger, err := bing.NewPinger(address)

		// Windows support
		pinger.SetPrivileged(true)

		if err != nil {
			fmt.Printf("Failed to create pinger for %s: %v\n", address, err)
			p.mu.Lock()
			p.Latencies[address] = -1
			p.mu.Unlock()
			continue
		}

		pinger.Count = 1
		pinger.Timeout = 2 * time.Second

		err = pinger.Run()
		if err != nil {
			fmt.Printf("Failed to ping %s: %v\n", address, err)
			p.mu.Lock()
			p.Latencies[address] = -1
			p.mu.Unlock()
			continue
		}

		stats := pinger.Statistics()

		p.mu.Lock()
		if stats.PacketsRecv > 0 {
			p.Latencies[address] = stats.AvgRtt
			fmt.Printf("Ping to %s: %v\n", address, stats.AvgRtt)
		} else {
			p.Latencies[address] = -1
			fmt.Printf("Ping to %s failed: No packets received\n", address)
		}
		p.mu.Unlock()
	}

	if p.DBEnabled {
		p.SaveLatenciesToDB()
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

	if err := p.DB.SavePingTime(p.Latencies); err != nil {
		log.Printf("Failed to save latencies to the database: %v", err)
	} else {
		fmt.Println("Successfully saved latencies to the database.")
	}
}

func (p *Pinger) Run() {
	go func() {
		p.updateAddresses()
		fmt.Println("Initial node discovery completed.")

		ticker := time.NewTicker(p.Interval)
		defer ticker.Stop()

		// Ticker for periodic pinging
		pingTicker := time.NewTicker(p.Interval)
		defer pingTicker.Stop()

		// Ticker for periodic discovery
		discoverTicker := time.NewTicker(20 * time.Second)
		defer discoverTicker.Stop()

		for {
			select {
			case <-ticker.C:
				p.PingAll()
				fmt.Println("Pinged all addresses")

			case <-discoverTicker.C:
				p.updateAddresses()
				fmt.Println("Updated node addresses")

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
