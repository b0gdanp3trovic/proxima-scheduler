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
	"sync"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
)

type ConsulServiceInstance struct {
	Service struct {
		Address string            `json:"Address"`
		Port    int               `json:"Port"`
		Meta    map[string]string `json:"Meta"`
	} `json:"Service"`
}

type EdgeProxy struct {
	proxy         *httputil.ReverseProxy
	consulAddress string
	database      util.Database
	cache         map[string]cachedPod
	cacheMutex    sync.RWMutex
	cacheDuration time.Duration
}

type cachedPod struct {
	pod        ConsulServiceInstance
	expiration time.Time
}

func NewEdgeProxy(consulAddress string, worker *LatencyWorker, db util.Database, cacheDuration time.Duration) *EdgeProxy {
	return &EdgeProxy{
		proxy: &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				podUrl := req.Context().Value("pod_url").(string)

				// Adjust the path
				parts := strings.Split(req.URL.Path, "/")
				if len(parts) >= 2 {
					// Path is like '/service/endpoint/...', forward it as '/endpoint/...'
					req.URL.Path = "/" + strings.Join(parts[2:], "/")
				} else {
					// Path is just '/service', forward it as '/'
					req.URL.Path = "/"
				}

				// Forward the request to the pod
				req.URL.Scheme = "http"
				req.URL.Host = podUrl
				log.Printf("Forwarding request to %s", podUrl)
			},
			ModifyResponse: func(resp *http.Response) error {
				// Measure latency and log it
				latency := time.Since(resp.Request.Context().Value("start_time").(time.Time))
				podUrl := resp.Request.Context().Value("pod_url").(string)
				nodeIP := resp.Request.Context().Value("node_ip").(string)
				serviceName := resp.Request.URL.Path

				worker.SendLatencyData(LatencyData{
					ServiceName: serviceName,
					PodURL:      podUrl,
					NodeIP:      nodeIP,
					Latency:     latency,
					Timestamp:   time.Now(),
				})

				return nil
			},
		},
		consulAddress: consulAddress,
		database:      db,
		cache:         make(map[string]cachedPod),
		cacheDuration: cacheDuration,
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

func (ep *EdgeProxy) getBestPod(serviceName string) (ConsulServiceInstance, error) {
	ep.cacheMutex.RLock()
	// We already have a pod in cache for a given service name
	// and it's still valid.
	if cached, found := ep.cache[serviceName]; found && time.Now().Before(cached.expiration) {
		ep.cacheMutex.RUnlock()
		log.Printf("Returning cached pod for service %s", serviceName)
		return cached.pod, nil
	}
	ep.cacheMutex.RUnlock()

	pods, err := getServicePodsFromConsul(serviceName, ep.consulAddress)
	if err != nil || len(pods) == 0 {
		return ConsulServiceInstance{}, fmt.Errorf("failed to obtain pods from Consul")
	}

	averageLatencies, err := ep.database.GetAveragePingTime()
	if err != nil {
		return ConsulServiceInstance{}, fmt.Errorf("Failed to retrieve average latencies.")
	}

	var bestPod ConsulServiceInstance
	var lowestLatency float64
	latencyFound := false

	for _, pod := range pods {
		nodeIP, exists := pod.Service.Meta["node_ip"]
		if !exists {
			continue
		}

		latency, err := getLatencyForNode(nodeIP, averageLatencies)
		if err != nil {
			log.Printf("Failed to get latency for node %s: %v", nodeIP, err)
			continue
		}

		if !latencyFound || latency < lowestLatency {
			lowestLatency = latency
			bestPod = pod
			latencyFound = true
		}
	}

	if !latencyFound {
		return ConsulServiceInstance{}, fmt.Errorf("no suitable pod found based on latency data")
	}

	ep.cacheMutex.Lock()
	ep.cache[serviceName] = cachedPod{
		pod:        bestPod,
		expiration: time.Now().Add(ep.cacheDuration),
	}
	ep.cacheMutex.Unlock()

	log.Printf("Selected and cached pod for service %s", serviceName)
	return bestPod, nil
}

func getLatencyForNode(nodeIP string, averageLatencies map[string]float64) (float64, error) {
	latency, exists := averageLatencies[nodeIP]
	if !exists {
		return 0, fmt.Errorf("latency data not found for node %s", nodeIP)
	}
	return latency, nil
}

func preprocessRequest(ep *EdgeProxy) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 2 || parts[1] == "" {
			http.Error(w, "Invalid request path", http.StatusBadRequest)
			return
		}

		serviceName := parts[1]

		// Select best pod
		pod, err := ep.getBestPod(serviceName)
		if err != nil {
			http.Error(w, "Failed obtaining the best pod.", http.StatusInternalServerError)
		}

		podUrl := fmt.Sprintf("%s:%d", pod.Service.Address, pod.Service.Port)
		nodeIP, exists := pod.Service.Meta["node_ip"]
		if !exists {
			http.Error(w, "node_ip not found in service metadata", http.StatusInternalServerError)
			return
		}

		ctx := context.WithValue(r.Context(), "pod_url", podUrl)
		ctx = context.WithValue(ctx, "node_ip", nodeIP)
		ctx = context.WithValue(ctx, "start_time", time.Now())

		log.Printf("Forwarding request to pod %s on node %s", podUrl, nodeIP)

		ep.proxy.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (ep *EdgeProxy) Run() {
	// Start the proxy server in a goroutine
	go func() {
		log.Println("Edge proxy server is starting on port 8080...")
		log.Printf("Consul instance on %s...", ep.consulAddress)
		http.Handle("/", preprocessRequest(ep))

		err := http.ListenAndServe(":8080", nil)
		if err != nil {
			log.Fatalf("Error starting proxy server: %v", err)
		}
	}()
}
