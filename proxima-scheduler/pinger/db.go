package pinger

import (
	"fmt"
	"time"

	client "github.com/influxdata/influxdb1-client/v2"
)

type Database interface {
	SavePingTime(latencies map[string]time.Duration) error
	GetAveragePingTime() (map[string]float64, error)
}

type InfluxDB struct {
	DatabaseName string
	Client       client.Client
}

func NewInfluxDB(client client.Client, databaseName string) *InfluxDB {
	return &InfluxDB{
		DatabaseName: databaseName,
		Client:       client,
	}
}

func (db *InfluxDB) SavePingTime(latencies map[string]time.Duration) error {
	bp, err := client.NewBatchPoints(client.BatchPointsConfig{
		Database:  db.DatabaseName,
		Precision: "s",
	})

	if err != nil {
		return err
	}

	for address, latency := range latencies {
		tags := map[string]string{
			"node": address,
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

func (db *InfluxDB) GetAveragePingTime() (map[string]float64, error) {
	query := fmt.Sprintf(`
		SELECT MEAN("latency_ms")
		FROM "ping_times"
		WHERE time > now() - 30s
		GROUP BY "node"
	`)

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
		node := row.Tags["node"]
		fmt.Printf("InfluxDB node name: %s\n", node) // Print InfluxDB node names
		if len(row.Values) > 0 {
			meanLatency, ok := row.Values[0][1].(float64)
			if ok {
				result[node] = meanLatency
			}
		}
	}

	return result, nil
}
