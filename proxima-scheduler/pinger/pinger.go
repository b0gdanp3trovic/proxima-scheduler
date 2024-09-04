package pinger

import (
	"fmt"
	"log"
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
}

func NewPinger(interval time.Duration, clientset *kubernetes.Clientset, dbEnabled bool, db Database) (*Pinger, error) {
	p := &Pinger{
		Addr:      make(map[string]struct{}),
		Latencies: make(map[string]time.Duration),
		Interval:  interval,
		stopChan:  make(chan struct{}),
		DBEnabled: dbEnabled,
		DB:        db,
		Clientset: clientset,
	}

	return p, nil
}

func (p *Pinger) AddAddress(address string) {
	p.Addr[address] = struct{}{}
	p.Latencies[address] = 0
}

func (p *Pinger) RemoveAddress(address string) {
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
	for address := range p.Addr {
		// Bing pinger
		pinger, err := bing.NewPinger(address)
		if err != nil {
			fmt.Printf("Failed to create pinger for %s: %v\n", address, err)
			p.Latencies[address] = -1
			continue
		}

		pinger.Count = 1
		pinger.Timeout = 2 * time.Second
		err = pinger.Run()
		if err != nil {
			fmt.Printf("Failed to ping %s: %v\n", address, err)
			p.Latencies[address] = -1
			continue
		}

		stats := pinger.Statistics()
		if stats.PacketsRecv > 0 {
			p.Latencies[address] = stats.AvgRtt
			fmt.Printf("Ping to %s: %v\n", address, stats.AvgRtt)
		} else {
			p.Latencies[address] = -1
			fmt.Printf("Ping to %s failed: No packets received\n", address)
		}
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

	// Clear latencies map
	p.Latencies = nil
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
