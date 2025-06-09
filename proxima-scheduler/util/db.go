package util

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/influxdata/influxdb-client-go/v2/api"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

type Database interface {
	SavePingTime(latencies map[string]time.Duration, edgeProxyAddress string) error
	SaveAggregatedLatencies(latencies map[AggregatedLatencyKey]time.Duration) error
	GetAveragePingTime() (NodeLatencies, error)
	GetAveragePingTimeByEdges() (EdgeProxyToNodeLatencies, error)
	GetAverageLatenciesForEdge(edgeProxyAddress string) (NodeLatencies, error)
	SaveEdgeProxyMetricsForService(serviceName string, podURL string, edgeProxyIP string, avgLatency time.Duration, avgRPM float64) error
	GetNodeScores() (NodeScores, error)
	GetNodeScore(nodeIP string) (float64, error)
	SaveRequestLatency(podURL string, nodeIP string, edgeproxyNodeIP string, latency time.Duration) error
	SaveNodeScores(scores map[string]float64) error
	GetLatency(source, destination string) (time.Duration, error)
	GetTotalRPMByEdgeProxy() (map[string]float64, error)
}

type InfluxDB struct {
	Bucket   string
	Org      string
	Client   influxdb2.Client
	WriteAPI api.WriteAPI
	QueryAPI api.QueryAPI
	initOnce sync.Once
}

type NodeLatencies map[string]float64
type EdgeProxyToNodeLatencies map[string]map[string]float64
type NodeScores map[string]float64

type AggregatedLatencyKey struct {
	Source      string
	Destination string
}

func NewInfluxDB(client influxdb2.Client, org, bucket string) *InfluxDB {
	db := &InfluxDB{
		Bucket:   bucket,
		Org:      org,
		Client:   client,
		WriteAPI: client.WriteAPI(org, bucket),
		QueryAPI: client.QueryAPI(org),
	}

	go func() {
		for err := range db.WriteAPI.Errors() {
			log.Printf("[InfluxDB] async write error: %v", err)
		}
	}()

	return db
}

func (db *InfluxDB) SavePingTime(latencies map[string]time.Duration, edgeProxyAddress string) error {
	for address, latency := range latencies {
		point := influxdb2.NewPoint(
			"ping_times",
			map[string]string{
				"node":       address,
				"edge_proxy": edgeProxyAddress,
			},
			map[string]interface{}{
				"latency_ms": latency.Seconds() * 1000,
			},
			time.Now(),
		)
		db.WriteAPI.WritePoint(point)
	}

	db.WriteAPI.Flush()
	return nil
}

func (db *InfluxDB) SaveAggregatedLatencies(latencies map[AggregatedLatencyKey]time.Duration) error {
	for key, latency := range latencies {
		point := influxdb2.NewPoint(
			"ping_times",
			map[string]string{
				"node":       key.Destination,
				"edge_proxy": key.Source,
			},
			map[string]interface{}{
				"latency_ms": latency.Seconds() * 1000,
			},
			time.Now(),
		)

		db.WriteAPI.WritePoint(point)
	}

	db.WriteAPI.Flush()
	return nil
}

func (db *InfluxDB) SaveRequestLatency(podURL, nodeIP, edgeproxyNodeIP string, latency time.Duration) error {
	point := influxdb2.NewPoint(
		"request_latency",
		map[string]string{
			"node":      nodeIP,
			"pod":       podURL,
			"edge_node": edgeproxyNodeIP,
		},
		map[string]interface{}{
			"latency_ms": latency.Seconds() * 1000,
		},
		time.Now(),
	)

	db.WriteAPI.WritePoint(point)
	db.WriteAPI.Flush()
	return nil
}

func (db *InfluxDB) GetAveragePingTime() (NodeLatencies, error) {
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -30s)
		|> filter(fn: (r) => r._measurement == "ping_times")
		|> group(columns: ["node"])
		|> mean(column: "_value")
	`, db.Bucket)

	result, err := db.QueryAPI.Query(context.Background(), query)
	if err != nil {
		return nil, fmt.Errorf("failed to query average ping times: %w", err)
	}

	latencies := make(NodeLatencies)
	for result.Next() {
		node, ok := result.Record().ValueByKey("node").(string)
		if !ok {
			log.Printf("Failed to parse node tag in record: %+v", result.Record())
			continue
		}

		latency, ok := result.Record().Value().(float64)
		if !ok {
			log.Printf("Failed to parse latency for node %s in record: %+v", node, result.Record())
			continue
		}

		latencies[node] = latency
	}

	if result.Err() != nil {
		return nil, fmt.Errorf("query result error: %w", result.Err())
	}

	return latencies, nil
}

func (db *InfluxDB) GetAveragePingTimeByEdges() (EdgeProxyToNodeLatencies, error) {
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -2m)
		|> filter(fn: (r) => r._measurement == "ping_times")
		|> group(columns: ["node", "edge_proxy"])
		|> mean(column: "_value")
	`, db.Bucket)

	result, err := db.QueryAPI.Query(context.Background(), query)
	if err != nil {
		return nil, fmt.Errorf("failed to query average ping times by edges: %w", err)
	}

	latencies := make(EdgeProxyToNodeLatencies)
	for result.Next() {
		node, ok := result.Record().ValueByKey("node").(string)
		if !ok {
			log.Printf("Failed to parse node tag in record: %+v", result.Record())
			continue
		}

		edgeProxy, ok := result.Record().ValueByKey("edge_proxy").(string)
		if !ok {
			log.Printf("Failed to parse edge_proxy tag in record: %+v", result.Record())
			continue
		}

		latency, ok := result.Record().Value().(float64)
		if !ok {
			log.Printf("Failed to parse latency for node %s and edge proxy %s in record: %+v", node, edgeProxy, result.Record())
			continue
		}

		if latencies[edgeProxy] == nil {
			latencies[edgeProxy] = make(map[string]float64)
		}

		latencies[edgeProxy][node] = latency
	}

	if result.Err() != nil {
		return nil, fmt.Errorf("query result error: %w", result.Err())
	}

	return latencies, nil
}

func (db *InfluxDB) GetAverageLatenciesForEdge(edgeProxyAddress string) (NodeLatencies, error) {
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -30s)
		|> filter(fn: (r) => r._measurement == "ping_times")
		|> filter(fn: (r) => r.edge_proxy == "%s")
		|> group(columns: ["node"])
		|> mean(column: "_value")
	`, db.Bucket, edgeProxyAddress)

	result, err := db.QueryAPI.Query(context.Background(), query)
	if err != nil {
		return nil, fmt.Errorf("failed to query average latencies for edge: %w", err)
	}

	latencies := make(NodeLatencies)
	for result.Next() {
		node, ok := result.Record().ValueByKey("node").(string)
		if !ok {
			log.Printf("Failed to parse node tag in record: %+v", result.Record())
			continue
		}

		latency, ok := result.Record().Value().(float64)
		if !ok {
			log.Printf("Failed to parse latency for node %s in record: %+v", node, result.Record())
			continue
		}

		latencies[node] = latency
	}

	if result.Err() != nil {
		return nil, fmt.Errorf("query result error for edge proxy %s: %w", edgeProxyAddress, result.Err())
	}

	return latencies, nil
}

func (db *InfluxDB) GetNodeScores() (NodeScores, error) {
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -60m)
		|> filter(fn: (r) => r._measurement == "node_scores")
		|> group(columns: ["node"])
		|> last(column: "_value")
	`, db.Bucket)

	result, err := db.QueryAPI.Query(context.Background(), query)
	if err != nil {
		return nil, fmt.Errorf("failed to query node scores: %w", err)
	}

	scores := make(NodeScores)
	for result.Next() {
		node, ok := result.Record().ValueByKey("node").(string)
		if !ok {
			log.Printf("Failed to parse node tag in record: %+v", result.Record())
			continue
		}

		score, ok := result.Record().Value().(float64)
		if !ok {
			log.Printf("Failed to parse score for node %s in record: %+v", node, result.Record())
			continue
		}

		scores[node] = score
	}

	if result.Err() != nil {
		return nil, fmt.Errorf("query result error: %w", result.Err())
	}

	return scores, nil
}

func (db *InfluxDB) GetNodeScore(nodeIP string) (float64, error) {
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -60m)
		|> filter(fn: (r) => r._measurement == "node_scores" and r.node == "%s")
		|> last(column: "_value")
	`, db.Bucket, nodeIP)

	result, err := db.QueryAPI.Query(context.Background(), query)
	if err != nil {
		return 0, fmt.Errorf("failed to query node score for node '%s': %w", nodeIP, err)
	}

	var score float64
	found := false

	for result.Next() {
		val, ok := result.Record().Value().(float64)
		if !ok {
			log.Printf("Failed to parse score in record: %+v", result.Record())
			continue
		}
		score = val
		found = true
		break
	}

	if result.Err() != nil {
		return 0, fmt.Errorf("query result error for node '%s': %w", nodeIP, result.Err())
	}

	if !found {
		return 0, fmt.Errorf("no score found for node '%s' in the time range", nodeIP)
	}

	return score, nil
}

func (db *InfluxDB) SaveNodeScores(scores map[string]float64) error {
	for nodeIP, score := range scores {
		point := influxdb2.NewPoint(
			"node_scores",
			map[string]string{
				"node": nodeIP,
			},
			map[string]interface{}{
				"score": score,
			},
			time.Now(),
		)
		db.WriteAPI.WritePoint(point)
	}

	db.WriteAPI.Flush()
	return nil
}

func (db *InfluxDB) SaveEdgeProxyMetricsForService(serviceName string, podURL string, edgeProxyIP string, avgLatency time.Duration, avgRPM float64) error {
	point := influxdb2.NewPoint(
		"edge_proxy_service_metrics",
		map[string]string{
			"service":    serviceName,
			"pod_url":    podURL,
			"edge_proxy": edgeProxyIP,
		},
		map[string]interface{}{
			"avg_latency_ms": avgLatency.Seconds() * 1000,
			"avg_rpm":        avgRPM,
		},
		time.Now(),
	)

	db.WriteAPI.WritePoint(point)
	db.WriteAPI.Flush()

	return nil
}

func (db *InfluxDB) GetLatency(source, destination string) (time.Duration, error) {
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -1h)
		|> filter(fn: (r) => r._measurement == "ping_times")
		|> filter(fn: (r) => r.edge_proxy == "%s" and r.node == "%s")
		|> last()
	`, db.Bucket, source, destination)

	result, err := db.QueryAPI.Query(context.Background(), query)
	if err != nil {
		return 0, fmt.Errorf("failed to query latency from %s to %s: %w", source, destination, err)
	}

	if result.Next() {
		latencyFloat, ok := result.Record().Value().(float64)
		if !ok {
			return 0, fmt.Errorf("failed to parse latency value for %s -> %s: %+v", source, destination, result.Record())
		}
		latency := time.Duration(latencyFloat * float64(time.Millisecond))

		return latency, nil
	}

	if result.Err() != nil {
		return 0, fmt.Errorf("query result error for %s -> %s: %w", source, destination, result.Err())
	}

	return 0, fmt.Errorf("no latency data found for %s -> %s", source, destination)
}

func (db *InfluxDB) GetTotalRPMByEdgeProxy() (map[string]float64, error) {
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -5m)
		|> filter(fn: (r) => r._measurement == "edge_proxy_service_metrics")
		|> filter(fn: (r) => r._field == "avg_rpm")
		|> group(columns: ["edge_proxy"])
		|> sum()
	`, db.Bucket)

	result, err := db.QueryAPI.Query(context.Background(), query)
	if err != nil {
		return nil, fmt.Errorf("failed to query RPM data: %w", err)
	}

	rpmByEdge := make(map[string]float64)
	for result.Next() {
		edgeProxy, ok := result.Record().ValueByKey("edge_proxy").(string)
		if !ok {
			log.Printf("Failed to parse edge_proxy tag in record: %+v", result.Record())
			continue
		}

		rpmSum, ok := result.Record().Value().(float64)
		if !ok {
			log.Printf("Failed to parse rpm value in record: %+v", result.Record())
			continue
		}

		rpmByEdge[edgeProxy] = rpmSum
	}

	if result.Err() != nil {
		return nil, fmt.Errorf("query result error: %w", result.Err())
	}

	return rpmByEdge, nil
}
