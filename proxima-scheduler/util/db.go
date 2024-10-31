package util

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	client "github.com/influxdata/influxdb1-client/v2"
)

type Database interface {
	SavePingTime(latencies map[string]time.Duration, edgeProxyAddress string) error
	GetAveragePingTime() (NodeLatencies, error)
	GetAveragePingTimeByEdges() (EdgeProxyToNodeLatencies, error)
	SaveRequestLatency(podURL, nodeIP, edgeproxyNodeIP string, latency time.Duration) error
}

type InfluxDB struct {
	DatabaseName string
	Client       client.Client
}

type NodeLatencies map[string]float64

type EdgeProxyToNodeLatencies map[string]map[string]float64

func NewInfluxDB(client client.Client, databaseName string) *InfluxDB {
	return &InfluxDB{
		DatabaseName: databaseName,
		Client:       client,
	}
}

func (db *InfluxDB) SavePingTime(latencies map[string]time.Duration, edgeProxyAddress string) error {
	bp, err := client.NewBatchPoints(client.BatchPointsConfig{
		Database:  db.DatabaseName,
		Precision: "s",
	})

	if err != nil {
		return err
	}

	for address, latency := range latencies {
		tags := map[string]string{
			"node":       address,
			"edge_proxy": edgeProxyAddress,
		}

		fields := map[string]interface{}{
			"latency_ms": latency.Seconds() * 1000,
		}

		pt, err := client.NewPoint("ping_times", tags, fields, time.Now())
		if err != nil {
			return err
		}

		bp.AddPoint(pt)
	}

	return db.Client.Write(bp)
}

func (db *InfluxDB) SaveRequestLatency(podURL, nodeIP, edgeproxyNodeIP string, latency time.Duration) error {
	bp, err := client.NewBatchPoints(client.BatchPointsConfig{
		Database:  db.DatabaseName,
		Precision: "s",
	})

	if err != nil {
		return err
	}

	tags := map[string]string{
		"node":      nodeIP,
		"pod":       podURL,
		"edge_node": edgeproxyNodeIP,
	}

	fields := map[string]interface{}{
		"latency_ms": latency.Seconds() * 1000,
	}

	pt, err := client.NewPoint("request_latency", tags, fields, time.Now())
	if err != nil {
		return err
	}

	bp.AddPoint(pt)

	return db.Client.Write(bp)
}

func (db *InfluxDB) GetAveragePingTime() (NodeLatencies, error) {
	query := fmt.Sprintf(`
		SELECT MEAN("latency_ms")
		FROM %s.autogen.ping_times
		WHERE time > now() - 30s
		GROUP BY "node"
	`, db.DatabaseName)

	q := client.NewQuery(query, db.DatabaseName, "s")
	response, err := db.Client.Query(q)
	if err != nil {
		return nil, err
	}

	if response.Error() != nil {
		return nil, response.Error()
	}

	result := make(map[string]float64)
	for _, row := range response.Results[0].Series {
		node := strings.TrimSpace(strings.ToLower(row.Tags["node"]))

		if len(row.Values) > 0 {
			latency, err := parseLatency(row.Values[0][1], node)
			if err != nil {
				fmt.Printf("Error parsing latency for node %s: %v\n", node, err)
				continue
			}
			result[node] = latency
		}
	}

	return result, nil
}

func (db *InfluxDB) GetAveragePingTimeByEdges() (EdgeProxyToNodeLatencies, error) {
	query := fmt.Sprintf(`
		SELECT MEAN("latency_ms")
		FROM %s.autogen.ping_times
		WHERE time > now() - 30s
		GROUP BY "node", "edge_proxy"
	`, db.DatabaseName)

	q := client.NewQuery(query, db.DatabaseName, "s")
	response, err := db.Client.Query(q)
	if err != nil {
		return nil, err
	}

	if response.Error() != nil {
		return nil, response.Error()
	}

	result := make(EdgeProxyToNodeLatencies)

	for _, row := range response.Results[0].Series {
		node := strings.TrimSpace(strings.ToLower(row.Tags["node"]))
		edgeProxy := strings.TrimSpace(strings.ToLower(row.Tags["edge_proxy"]))

		if result[edgeProxy] == nil {
			result[edgeProxy] = make(map[string]float64)
		}

		if len(row.Values) > 0 {
			latency, err := parseLatency(row.Values[0][1], node)
			if err != nil {
				fmt.Printf("Error parsing latency for node %s and edge proxy %s: %v\n", node, edgeProxy, err)
				continue
			}

			result[edgeProxy][node] = latency
		}
	}

	return result, nil
}

func parseLatency(latencyInterface interface{}, node string) (float64, error) {
	switch latencyInterface.(type) {
	case float64:
		return latencyInterface.(float64), nil
	case string:
		latencyStr := latencyInterface.(string)
		return strconv.ParseFloat(latencyStr, 64)
	case json.Number:
		return latencyInterface.(json.Number).Float64()
	default:
		return 0, fmt.Errorf("unexpected type for latency value on node %s: %T", node, latencyInterface)
	}
}
