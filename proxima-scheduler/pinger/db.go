package pinger

import (
	"time"

	client "github.com/influxdata/influxdb1-client/v2"
)

type Database interface {
	SavePingTime(latencies map[string]time.Duration) error
}

type InfluxDB struct {
	Client client.Client
}

func NewInfluxDB(client client.Client) *InfluxDB {
	return &InfluxDB{
		Client: client,
	}
}

func (db *InfluxDB) SavePingTime(latencies map[string]time.Duration) error {
	bp, err := client.NewBatchPoints(client.BatchPointsConfig{
		Database:  "ping_db",
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
