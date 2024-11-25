package edgeproxy

import (
	"log"
	"sync"
	"time"

	"github.com/b0gdanp3trovic/proxima-scheduler/util"
)

type MetricsData struct {
	ServiceName  string
	PodURL       string
	NodeIP       string
	Latency      time.Duration
	Timestamp    time.Time
	RequestCount int
}

type ProxyPodMetrics struct {
	TotalLatency time.Duration
	RequestCount int
	LastUpdated  time.Time
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
	log.Println("Starting latency worker...")
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
			}
		}

		metrics := mw.serviceMetrics[data.ServiceName][data.PodURL]
		metrics.TotalLatency += data.Latency
		metrics.RequestCount++
		metrics.LastUpdated = time.Now()

		mw.metricsMutex.Unlock()
	}
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
			avgLatency := time.Duration(0)
			if metrics.RequestCount > 0 {
				avgLatency = metrics.TotalLatency / time.Duration(metrics.RequestCount)
			}

			elapsedMinutes := now.Sub(metrics.LastUpdated).Minutes()
			if elapsedMinutes == 0 {
				elapsedMinutes = 1
			}

			avgRPM := float64(metrics.RequestCount) / elapsedMinutes
			log.Printf("Flushing metrics for service: %s, pod: %s, avg latency: %v, avg RPM: %.2f",
				serviceName, podURL, avgLatency, avgRPM)

			err := mw.database.SaveEdgeProxyMetricsForService(serviceName, podURL, mw.hostNodeIP, avgLatency, avgRPM)
			if err != nil {
				log.Printf("Failed to save metrics for service: %s, pod: %s, error: %v", serviceName, podURL, err)
			} else {
				log.Printf("Successfully saved metrics for service: %s, pod: %s", serviceName, podURL)
			}

			metrics.TotalLatency = 0
			metrics.RequestCount = 0

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
