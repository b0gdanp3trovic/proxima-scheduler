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
	DB        Database
}

func NewPinger(addresses []string, interval time.Duration, db Database) *Pinger {
	return &Pinger{
		Addr:      addresses,
		Latencies: make(map[string]time.Duration),
		Interval:  interval,
		stopChan:  make(chan struct{}),
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

func (p *Pinger) SaveLatenciesToDB(latencies map[string]time.Duration) {
	if len(latencies) == 0 {
		log.Printf("Empty latencies table, nothing to save.")
		return
	}

	if err := p.DB.SavePingTime(latencies); err != nil {
		log.Printf("Failed to save latencies to the database.")
		return
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
				fmt.Print("Pinged all addresses")

			case <-p.stopChan:
				fmt.Print("Stopping pinger.")
				p.DB.SavePingTime(p.Latencies)
				return
			}
		}
	}()
}

func (p *Pinger) Stop() {
	close(p.stopChan)
}
