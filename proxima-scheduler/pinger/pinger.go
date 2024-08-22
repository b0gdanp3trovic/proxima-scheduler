package pinger

import (
	"fmt"
	"log"
	"net"
	"time"
)

type Pinger struct {
	Addr      []string
	Latencies map[string]time.Duration
	Interval  time.Duration
	stopChan  chan struct{}
	DBEnabled bool
	DB        Database
}

func NewPinger(addresses []string, interval time.Duration, dbEnabled bool, db Database) *Pinger {
	return &Pinger{
		Addr:      addresses,
		Latencies: make(map[string]time.Duration),
		Interval:  interval,
		stopChan:  make(chan struct{}),
		DBEnabled: dbEnabled,
		DB:        db,
	}
}

func (p *Pinger) AddAddress(address string) {
	p.Addr = append(p.Addr, address)
}

func (p *Pinger) RemoveAddress(address string) {
	for i, addr := range p.Addr {
		if addr == address {
			p.Addr = append(p.Addr[:i], p.Addr[i+1:]...)
			delete(p.Latencies, address)
		}
	}
}

func (p Pinger) PingAll() {
	for _, address := range p.Addr {
		start := time.Now()
		conn, err := net.Dial("tcp", address)

		if err != nil {
			p.Latencies[address] = -1
			fmt.Printf("Failed to ping %s\n", address)
		}

		conn.Close()
		p.Latencies[address] = time.Since(start)
	}

	p.DB.SavePingTime(p.Latencies)
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
		ticker := time.NewTicker(p.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				p.PingAll()
				fmt.Println("Pinged all addresses")

			case <-p.stopChan:
				fmt.Println("Stopping pinger.")
				// Save any remaining latencies before stopping
				p.SaveLatenciesToDB()
				return
			}
		}
	}()
}

func (p *Pinger) Stop() {
	close(p.stopChan)
}
