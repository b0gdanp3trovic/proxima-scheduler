package edgeproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

type ConsulServiceInstance struct {
	Service struct {
		Address string `json:"Address"`
		Port    int    `json:"Port"`
	} `json:"Service"`
}

type EdgeProxy struct {
	proxy         *httputil.ReverseProxy
	consulAddress string
}

func NewEdgeProxy(consulAddress string) *EdgeProxy {
	return &EdgeProxy{
		proxy: &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				log.Println("Got a request")
				// Set start time in the request context
				ctx := context.WithValue(req.Context(), "start_time", time.Now())
				req = req.WithContext(ctx)

				parts := strings.Split(req.URL.Path, "/")
				if len(parts) < 2 || parts[1] == "" {
					log.Println("Invalid path, cannot extract service name")
					req.URL.Host = "" // Force a failure by clearing the Host
					return
				}

				serviceName := parts[1]
				pods, err := getServicePodsFromConsul(serviceName, consulAddress)
				if err != nil || len(pods) == 0 {
					log.Printf("Failed to get service instances from Consul: %v", err)
					req.URL.Host = ""
					return
				}

				pod := pods[0]

				req.URL.Scheme = "http"
				req.URL.Host = fmt.Sprintf("%s:%d", pod.Service.Address, pod.Service.Port)
				log.Printf("Forwarding request to %s:%d", pod.Service.Address, pod.Service.Port)
			},
			ModifyResponse: func(resp *http.Response) error {
				// Measure latency and log it
				latency := time.Since(resp.Request.Context().Value("start_time").(time.Time))
				println(latency)
				// log latency
				return nil
			},
		},
	}
}

func getServicePodsFromConsul(serviceName string, consulURL string) ([]ConsulServiceInstance, error) {
	resp, err := http.Get(fmt.Sprintf("%s/v1/health/service/%s?passing=true", consulURL, serviceName))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var pods []ConsulServiceInstance
	err = json.Unmarshal(body, &pods)
	if err != nil {
		return nil, err
	}

	return pods, nil
}

func (ep *EdgeProxy) Run() {
	// Start the proxy server in a goroutine
	go func() {
		log.Println("Edge proxy server is starting on port 8080...")

		// Set up the HTTP handler to use the proxy
		http.Handle("/", ep.proxy)

		// Start listening and serving on port 8080
		err := http.ListenAndServe(":8080", nil)
		if err != nil {
			log.Fatalf("Error starting proxy server: %v", err)
		}
	}()
}
