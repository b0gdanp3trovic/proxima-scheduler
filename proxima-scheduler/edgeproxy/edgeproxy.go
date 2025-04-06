package edgeproxy

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
)

type K8sPodInstance struct {
	PodIP   string
	NodeIP  string
	Node    string
	PodName string
	Port    int32
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
	clientsets    map[string]*kubernetes.Clientset
	namespace     string
}

type cachedPod struct {
	pod        K8sPodInstance
	expiration time.Time
}

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

// Ugly hardcoding for now.
var allowedServices = map[string]bool{
	"test-flask-service": true,
}

func isServiceAllowed(serviceName string) bool {
	_, exists := allowedServices[serviceName]
	return exists
}

func (rec *responseRecorder) WriteHeader(statusCode int) {
	rec.statusCode = statusCode
	rec.ResponseWriter.WriteHeader(statusCode)
}

func NewEdgeProxy(
	consulAddress string,
	worker *MetricsWorker,
	db util.Database,
	inClusterClientset *kubernetes.Clientset,
	kubeconfigs map[string]string,
	cacheDuration time.Duration,
	nodeIP string) (*EdgeProxy, error) {

	clientsets := make(map[string]*kubernetes.Clientset)

	for clusterName, kubeconfigPath := range kubeconfigs {
		clientset, err := util.GetClientsetForCluster(kubeconfigPath)
		if err != nil {
			log.Printf("Failed to load cluster %s: %v\n", clusterName, err)
			return nil, err
		}
		clientsets[clusterName] = clientset
	}

	clientsets["local"] = inClusterClientset

	if len(clientsets) == 0 {
		return nil, fmt.Errorf("No Kubernetes clusters available")
	}

	return &EdgeProxy{
		clientsets: clientsets,
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
				serviceName := resp.Request.Context().Value("service_name").(string)

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
		namespace:     "default",
	}, nil
}

func getPodsForService(serviceName, namespace string, clientset *kubernetes.Clientset) ([]K8sPodInstance, error) {
	labelSelector := fmt.Sprintf("app=%s", serviceName)
	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %v", err)
	}

	var results []K8sPodInstance

	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}

		if pod.Status.PodIP == "" || pod.Spec.NodeName == "" {
			continue
		}

		node, err := clientset.CoreV1().Nodes().Get(context.TODO(), pod.Spec.NodeName, metav1.GetOptions{})
		if err != nil {
			log.Printf("Failed to get node info for %s: %v", pod.Spec.NodeName, err)
			continue
		}

		nodeIP, err := util.GetNodeInternalIP(node)
		if err != nil {
			log.Printf("No internal IP found for node %s", node.Name)
			continue
		}

		results = append(results, K8sPodInstance{
			PodIP:   pod.Status.PodIP,
			Node:    pod.Spec.NodeName,
			NodeIP:  nodeIP,
			PodName: pod.Name,
			Port:    pod.Spec.Containers[0].Ports[0].ContainerPort,
		})
	}

	return results, nil
}

func (ep *EdgeProxy) getBestPod(serviceName string) (K8sPodInstance, error) {
	ep.cacheMutex.RLock()
	// We already have a pod in cache for a given service name
	// and it's still valid.
	if cached, found := ep.cache[serviceName]; found && time.Now().Before(cached.expiration) {
		ep.cacheMutex.RUnlock()
		log.Printf("Returning cached pod for service %s", serviceName)
		return cached.pod, nil
	}
	ep.cacheMutex.RUnlock()

	pods, err := getPodsForService(serviceName, ep.namespace, ep.clientsets["local"])

	if err != nil {
		return K8sPodInstance{}, fmt.Errorf("failed to obtain pods from local cluster: %v", err)
	}

	if len(pods) == 0 {
		return K8sPodInstance{}, fmt.Errorf("No valid pods in local cluster")
	}

	log.Printf("Pods: %v", pods)
	log.Printf("Edge proxy nodeIP: %v", ep.NodeIP)

	latenciesByEdge, err := ep.database.GetAverageLatenciesForEdge(ep.NodeIP)
	if err != nil {
		return K8sPodInstance{}, fmt.Errorf("Failed to retrieve average latencies: %v", err)
	}

	var bestPod K8sPodInstance
	var lowestLatency float64
	latencyFound := false

	log.Printf("Latencies by edge: %v", latenciesByEdge)
	for _, pod := range pods {
		latency, exists := latenciesByEdge[pod.NodeIP]
		if !exists {
			log.Printf("No latency data available for node %s", pod.NodeIP)
			continue
		}

		if !latencyFound || latency < lowestLatency {
			lowestLatency = latency
			bestPod = pod
			latencyFound = true
		}
	}

	if !latencyFound {
		return K8sPodInstance{}, fmt.Errorf("no suitable pod found based on latency data")
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

		if !isServiceAllowed(serviceName) {
			http.Error(w, "Service not allowed", http.StatusForbidden)
			return
		}

		// Select best pod
		pod, err := ep.getBestPod(serviceName)
		if err != nil {
			log.Printf("Failed obtaining the best pod: %v", err)
			http.Error(w, "Failed obtaining the best pod.", http.StatusInternalServerError)
			return
		}

		podUrl := fmt.Sprintf("%s:%d", pod.PodIP, pod.Port)

		ctx := context.WithValue(r.Context(), "pod_url", podUrl)
		ctx = context.WithValue(ctx, "node_ip", pod.NodeIP)
		ctx = context.WithValue(ctx, "start_time", time.Now())
		ctx = context.WithValue(ctx, "service_name", serviceName)

		recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		log.Printf("Forwarding request to pod %s on node %s", podUrl, pod.NodeIP)
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
		http.Handle("/", preprocessRequest(ep))

		err := http.ListenAndServe(":8080", nil)
		if err != nil {
			log.Fatalf("Error starting proxy server: %v", err)
		}
	}()
}
