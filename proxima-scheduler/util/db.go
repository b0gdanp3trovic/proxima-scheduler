package util

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	client "github.com/influxdata/influxdb1-client/v2"
)

type Database interface {
	SavePingTime(latencies map[string]time.Duration, edgeProxyAddress string) error
	GetAveragePingTime() (NodeLatencies, error)
	GetAveragePingTimeByEdges() (EdgeProxyToNodeLatencies, error)
	GetLatenciesForEdgeNode(edgeProxyAddress string) (NodeLatencies, error)
	GetNodeScores() (NodeScores, error)
	SaveRequestLatency(podURL string, nodeIP string, edgeproxyNodeIP string, latency time.Duration) error
	SaveNodeScores(scores map[string]float64) error
	createDbIfNotExists() error
}

type InfluxDB struct {
	DatabaseName string
	Client       client.Client
	initOnce     sync.Once
}

type NodeLatencies map[string]float64

type EdgeProxyToNodeLatencies map[string]map[string]float64

type NodeScores map[string]float64

func NewInfluxDB(client client.Client, databaseName string) (*InfluxDB, error) {
	db := &InfluxDB{
		DatabaseName: databaseName,
		Client:       client,
	}

	if err := db.createDbIfNotExists(); err != nil {
		return nil, err
	}

	return db, nil
}

func (db *InfluxDB) createDbIfNotExists() error {
	var err error
	db.initOnce.Do(func() {
		q := client.NewQuery(fmt.Sprintf("CREATE DATABASE %s", db.DatabaseName), "", "")
		log.Printf("Executing database creation query: %s", q.Command)
		response, queryErr := db.Client.Query(q)
		if queryErr != nil {
			err = fmt.Errorf("failed to execute database creation query: %w", queryErr)
			log.Printf("Query execution error: %v", queryErr)
			return
		}
		if response.Error() != nil {
			err = fmt.Errorf("failed to create database %s: %w", db.DatabaseName, response.Error())
			log.Printf("Query response error: %v", response.Error())
		}
		log.Printf("Database creation query completed successfully.")
	})
	return err
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

func (db *InfluxDB) SaveRequestLatency(podURL string, nodeIP string, edgeproxyNodeIP string, latency time.Duration) error {
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

func (db *InfluxDB) GetLatenciesForEdgeNode(edgeProxyAddress string) (NodeLatencies, error) {
	query := fmt.Sprintf(`
		SELECT "latency_ms"
		FROM %s.autogen.ping_times
		WHERE "edge_proxy" = '%s'
	`, db.DatabaseName, edgeProxyAddress)

	q := client.NewQuery(query, db.DatabaseName, "s")
	response, err := db.Client.Query(q)
	if err != nil {
		return nil, fmt.Errorf("failed to query latencies for edge node %s: %w", edgeProxyAddress, err)
	}

	if response.Error() != nil {
		return nil, fmt.Errorf("query response error for edge node %s: %w", edgeProxyAddress, response.Error())
	}

	latencies := make(NodeLatencies)
	for _, row := range response.Results[0].Series {
		node := strings.TrimSpace(strings.ToLower(row.Tags["node"]))

		for _, value := range row.Values {
			if len(value) > 1 {
				latency, err := parseLatency(value[1], node)
				if err != nil {
					fmt.Printf("Error parsing latency for node %s and edge proxy %s: %v\n", node, edgeProxyAddress, err)
					continue
				}
				latencies[node] = latency
			}
		}
	}

	return latencies, nil
}

func (db *InfluxDB) GetNodeScores() (NodeScores, error) {
	query := fmt.Sprintf(`
		SELECT LAST("score")
		FROM %s.autogen.node_scores
		GROUP BY "node"
	`, db.DatabaseName)

	q := client.NewQuery(query, db.DatabaseName, "s")
	response, err := db.Client.Query(q)

	if err != nil {
		return nil, fmt.Errorf("failed to query node scores: %w", err)
	}

	if response.Error() != nil {
		return nil, fmt.Errorf("query response error: %w", response.Error())
	}

	scores := make(NodeScores)
	for _, row := range response.Results[0].Series {
		node := strings.TrimSpace(strings.ToLower(row.Tags["node"]))

		if len(row.Values) > 0 {
			score, err := parseScore(row.Values[0][1], node)
			if err != nil {
				fmt.Printf("Error parsing score for node %s: %v\n", node, err)
				continue
			}
			scores[node] = score
		}
	}

	return scores, nil
}

func (db *InfluxDB) SaveNodeScores(scores map[string]float64) error {
	bp, err := client.NewBatchPoints(client.BatchPointsConfig{
		Database:  db.DatabaseName,
		Precision: "s",
	})
	if err != nil {
		return fmt.Errorf("failed to create batch points: %w", err)
	}

	for nodeIP, score := range scores {
		tags := map[string]string{
			"node": nodeIP,
		}
		fields := map[string]interface{}{
			"score": score,
		}

		pt, err := client.NewPoint("node_scores", tags, fields, time.Now())
		if err != nil {
			return fmt.Errorf("failed to create point for node %s: %w", nodeIP, err)
		}
		bp.AddPoint(pt)
	}

	if err := db.Client.Write(bp); err != nil {
		return fmt.Errorf("failed to write batch points to InfluxDB: %w", err)
	}

	return nil
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

func parseScore(scoreInterface interface{}, node string) (float64, error) {
	switch scoreInterface.(type) {
	case float64:
		return scoreInterface.(float64), nil
	case string:
		scoreStr := scoreInterface.(string)
		return strconv.ParseFloat(scoreStr, 64)
	case json.Number:
		return scoreInterface.(json.Number).Float64()
	default:
		return 0, fmt.Errorf("unexpected type for score value on node %s: %T", node, scoreInterface)
	}
}
