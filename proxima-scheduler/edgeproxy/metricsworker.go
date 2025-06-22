package edgeproxy

import (
	"log"
	"sync"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
)

type MetricsData struct {
	ServiceName string
	PodURL      string
	NodeIP      string
	Latency     time.Duration
	Timestamp   time.Time
}

type ProxyPodMetrics struct {
	LastUpdated time.Time
	// last 5 minutes time buckets
	RPMBuckets     [5]int
	LatencyBuckets [5]time.Duration
	CurrentIndex   int
	LastBucket     time.Time
}

type ProxyPodMetricsData struct {
	AvgLatency time.Duration
	AvgRPM     float64
}

type MetricsWorker struct {
	metricsChan    chan MetricsData
	database       util.Database
	hostNodeIP     string
	kindNetworkIP  string
	serviceMetrics map[string]map[string]*ProxyPodMetrics
	metricsMutex   sync.Mutex
	flushInterval  time.Duration
}

func (mw *MetricsWorker) Start() {
	log.Println("Starting metrics worker...")
	go mw.processMetrics()
	go mw.periodicFlush()
}

func NewMetricsWorker(bufferSize int, db util.Database, hostNodeIP string, kindNetworkIP string, flushInterval time.Duration) *MetricsWorker {
	return &MetricsWorker{
		metricsChan:    make(chan MetricsData, bufferSize),
		database:       db,
		hostNodeIP:     hostNodeIP,
		kindNetworkIP:  kindNetworkIP,
		serviceMetrics: make(map[string]map[string]*ProxyPodMetrics),
		flushInterval:  flushInterval,
	}
}

func (mw *MetricsWorker) processMetrics() {
	for data := range mw.metricsChan {
		mw.metricsMutex.Lock()

		if _, exists := mw.serviceMetrics[data.ServiceName]; !exists {
			mw.serviceMetrics[data.ServiceName] = make(map[string]*ProxyPodMetrics)
		}

		if _, exists := mw.serviceMetrics[data.ServiceName][data.PodURL]; !exists {
			mw.serviceMetrics[data.ServiceName][data.PodURL] = &ProxyPodMetrics{
				LastUpdated: time.Now(),
				LastBucket:  time.Now(),
			}
		}

		metrics := mw.serviceMetrics[data.ServiceName][data.PodURL]
		metrics.LastUpdated = time.Now()

		now := time.Now()
		elapsedMinutes := int(now.Sub(metrics.LastBucket).Minutes())

		// shift
		if elapsedMinutes > 0 {
			for i := 0; i < elapsedMinutes && i < 5; i++ {
				metrics.CurrentIndex = (metrics.CurrentIndex + 1) % 5
				metrics.RPMBuckets[metrics.CurrentIndex] = 0
				metrics.LatencyBuckets[metrics.CurrentIndex] = 0
			}
			metrics.LastBucket = now
		}
		metrics.RPMBuckets[metrics.CurrentIndex]++
		metrics.LatencyBuckets[metrics.CurrentIndex] += data.Latency

		mw.metricsMutex.Unlock()

		log.Printf("[DEBUG] Received latency data: service=%s, pod=%s, latency=%v",
			data.ServiceName, data.PodURL, data.Latency)

		log.Printf("[DEBUG] Updated RPMBuckets: %+v", metrics.RPMBuckets)
		log.Printf("[DEBUG] Updated LatencyBuckets: %+v", metrics.LatencyBuckets)
	}
}

func (mw *MetricsWorker) calculateAverageRPM(metrics *ProxyPodMetrics) float64 {
	totalRequests := 0

	for _, count := range metrics.RPMBuckets {
		totalRequests += count
	}

	return float64(totalRequests) / 5.0
}

func (mw *MetricsWorker) calculateAverageLatency(metrics *ProxyPodMetrics) time.Duration {
	totalLatency := time.Duration(0)
	totalRequests := 0

	for i := 0; i < len(metrics.RPMBuckets); i++ {
		totalLatency += metrics.LatencyBuckets[i]
		totalRequests += metrics.RPMBuckets[i]
	}

	if totalRequests == 0 {
		return 0
	}

	return totalLatency / time.Duration(totalRequests)
}

func (mw *MetricsWorker) periodicFlush() {
	ticker := time.NewTicker(mw.flushInterval)
	defer ticker.Stop()

	for range ticker.C {
		mw.flushMetrics()
	}
}

func (mw *MetricsWorker) flushMetrics() {
	mw.metricsMutex.Lock()
	defer mw.metricsMutex.Unlock()

	now := time.Now()
	staleThreshold := 15 * time.Minute

	for serviceName, podMetrics := range mw.serviceMetrics {
		for podURL, metrics := range podMetrics {

			avgLatency := mw.calculateAverageLatency(metrics)
			avgRPM := mw.calculateAverageRPM(metrics)

			log.Printf("avglatency: %v", avgLatency)
			log.Printf("avgrpm: %.2f", avgRPM)

			log.Printf("Flushing metrics for service: %s, pod: %s, avg latency: %v, avg RPM: %.2f",
				serviceName, podURL, avgLatency, avgRPM)

			mw.saveEdgeProxyMetrics(serviceName, podURL, mw.hostNodeIP, mw.kindNetworkIP, avgLatency, avgRPM)

			metrics.CurrentIndex = (metrics.CurrentIndex + 1) % len(metrics.RPMBuckets)
			metrics.RPMBuckets[metrics.CurrentIndex] = 0
			metrics.LatencyBuckets[metrics.CurrentIndex] = 0

			if now.Sub(metrics.LastUpdated) > staleThreshold {
				log.Printf("Removing stale metrics for service: %s, pod: %s", serviceName, podURL)
				delete(podMetrics, podURL)
			}
		}

		if len(podMetrics) == 0 {
			delete(mw.serviceMetrics, serviceName)
		}
	}

	var totalRequests int
	var totalLatency time.Duration

	for _, podMetrics := range mw.serviceMetrics {
		for _, metrics := range podMetrics {
			for i := 0; i < len(metrics.RPMBuckets); i++ {
				totalRequests += metrics.RPMBuckets[i]
				totalLatency += metrics.LatencyBuckets[i]
			}
		}
	}

	if totalRequests > 0 {
		avgLatency := totalLatency / time.Duration(totalRequests)
		avgRPM := float64(totalRequests) / 5.0

		log.Printf("Flushing aggregated metrics for edge proxy %s: total avg latency=%v, total avg RPM=%.2f",
			mw.hostNodeIP, avgLatency, avgRPM)

		mw.saveEdgeProxyMetrics("ALL", "ALL", mw.hostNodeIP, mw.kindNetworkIP, avgLatency, avgRPM)
	}
}

func (mw *MetricsWorker) SendLatencyData(data MetricsData) {
	select {
	case mw.metricsChan <- data:
	default:
		log.Printf("Latency channel full, dropping data: %v", data)
	}
}

func (mw *MetricsWorker) saveEdgeProxyMetrics(
	serviceName string,
	podURL string,
	host string,
	kindHost string,
	avgLatency time.Duration,
	avgRPM float64,
) {
	if kindHost == "" {
		err := mw.database.SaveEdgeProxyMetricsForService(serviceName, podURL, host, avgLatency, avgRPM)
		if err != nil {
			log.Printf("Failed to save aggregated metrics for edge proxy %s: %v", host, err)
		} else {
			log.Printf("Successfully saved aggregated metrics for edge proxy %s", host)
		}
	} else {
		err := mw.database.SaveEdgeProxyMetricsForService(serviceName, podURL, kindHost, avgLatency, avgRPM)
		if err != nil {
			log.Printf("Failed to save aggregated metrics for edge proxy %s: %v", kindHost, err)
		} else {
			log.Printf("Successfully saved aggregated metrics for edge proxy %s", kindHost)
		}
	}
}
