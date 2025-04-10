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

type MetricsWorker struct {
	metricsChan    chan MetricsData
	database       util.Database
	hostNodeIP     string
	serviceMetrics map[string]map[string]*ProxyPodMetrics
	metricsMutex   sync.Mutex
	flushInterval  time.Duration
}

func (mw *MetricsWorker) Start() {
	log.Println("Starting metrics worker...")
	go mw.processMetrics()
	go mw.periodicFlush()
}

func NewMetricsWorker(bufferSize int, db util.Database, hostNodeIP string, flushInterval time.Duration) *MetricsWorker {
	return &MetricsWorker{
		metricsChan:    make(chan MetricsData, bufferSize),
		database:       db,
		hostNodeIP:     hostNodeIP,
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

			err := mw.database.SaveEdgeProxyMetricsForService(serviceName, podURL, mw.hostNodeIP, avgLatency, avgRPM)
			if err != nil {
				log.Printf("Failed to save metrics for service: %s, pod: %s, error: %v", serviceName, podURL, err)
			} else {
				log.Printf("Successfully saved metrics for service: %s, pod: %s", serviceName, podURL)
			}

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
}

func (mw *MetricsWorker) SendLatencyData(data MetricsData) {
	select {
	case mw.metricsChan <- data:
	default:
		log.Printf("Latency channel full, dropping data: %v", data)
	}
}
