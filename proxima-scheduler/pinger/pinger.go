package pinger

import (
	"fmt"
	"net"
	"time"
)

type Pinger struct {
	Addr      []string
	Latencies map[string]time.Duration
	Interval  time.Duration
	stopChan  chan struct{}
}

func NewPinger(addresses []string, interval time.Duration) *Pinger {
	return &Pinger{
		Addr:      addresses,
		Latencies: make(map[string]time.Duration),
		Interval:  interval,
		stopChan:  make(chan struct{}),
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
				return
			}
		}
	}()
}

func (p *Pinger) Stop() {
	close(p.stopChan)
}
