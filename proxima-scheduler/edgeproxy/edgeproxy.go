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
	NodeIP        string
	requestCounts map[string]int
	requestMutex  sync.Mutex
}

type cachedPod struct {
	pod        ConsulServiceInstance
	expiration time.Time
}

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rec *responseRecorder) WriteHeader(statusCode int) {
	rec.statusCode = statusCode
	rec.ResponseWriter.WriteHeader(statusCode)
}

func NewEdgeProxy(consulAddress string, worker *MetricsWorker, db util.Database, cacheDuration time.Duration, nodeIP string) *EdgeProxy {
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

				// Save service name in the header,
				// context is not propagated from Director to ModifyResponse
				req.Header.Set("X-Proxima-Service-Name", parts[1])

				req.URL.Scheme = "http"
				req.URL.Host = podUrl
				log.Printf("Forwarding request to %s", podUrl)

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

				serviceName := resp.Request.Header.Get("X-Proxima-Service-Name")

				worker.SendLatencyData(MetricsData{
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
		NodeIP:        nodeIP,
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

	if len(pods) == 0 {
		return ConsulServiceInstance{}, fmt.Errorf("No valid pods found on Consul")
	}

	if err != nil {
		return ConsulServiceInstance{}, fmt.Errorf("failed to obtain pods from Consul: %v", err)
	}

	latenciesByEdge, err := ep.database.GetAverageLatenciesForEdge(ep.NodeIP)
	if err != nil {
		return ConsulServiceInstance{}, fmt.Errorf("Failed to retrieve average latencies: %v", err)
	}

	var bestPod ConsulServiceInstance
	var lowestLatency float64
	latencyFound := false

	for _, pod := range pods {
		nodeIP, exists := pod.Service.Meta["node_ip"]
		if !exists {
			continue
		}

		latency, exists := latenciesByEdge[nodeIP]
		if !exists {
			log.Printf("No latency data available for node %s", nodeIP)
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
	log.Printf("Avg latencies: %v", averageLatencies)
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
			log.Printf("Failed obtaining the best pod: %v", err)
			http.Error(w, "Failed obtaining the best pod.", http.StatusInternalServerError)
			return
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

		recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		log.Printf("Forwarding request to pod %s on node %s", podUrl, nodeIP)
		ep.proxy.ServeHTTP(recorder, r.WithContext(ctx))

		if recorder.statusCode >= http.StatusInternalServerError {
			log.Printf("Request to pod %s failed with status %d, invalidating cache for service %s", podUrl, recorder.statusCode, serviceName)
			ep.invalidateCache(serviceName)
			http.Error(w, "Failed to forward request to pod", http.StatusBadGateway)
		}
	})
}

func (ep *EdgeProxy) invalidateCache(serviceName string) {
	ep.cacheMutex.Lock()
	defer ep.cacheMutex.Unlock()
	delete(ep.cache, serviceName)
	log.Printf("Cache invalidated for service %s", serviceName)
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
