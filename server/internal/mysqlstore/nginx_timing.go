package mysqlstore

import (
	"context"

	"controltower/server/internal/storage"
)

func (s Store) UpsertNginxTimingBucket(v storage.NginxTimingBucket) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO nginx_timing_1m
(instance_id,bucket_at,request_count,upstream_count,status_4xx,status_5xx,status_504,rt_p50,rt_p95,rt_max,uht_p50,uht_p95,uht_max,transfer_p50,transfer_p95,transfer_max,bytes_total,slow_count,slow_ttft_count,slow_transfer_count)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE request_count=VALUES(request_count),upstream_count=VALUES(upstream_count),status_4xx=VALUES(status_4xx),status_5xx=VALUES(status_5xx),status_504=VALUES(status_504),rt_p50=VALUES(rt_p50),rt_p95=VALUES(rt_p95),rt_max=VALUES(rt_max),uht_p50=VALUES(uht_p50),uht_p95=VALUES(uht_p95),uht_max=VALUES(uht_max),transfer_p50=VALUES(transfer_p50),transfer_p95=VALUES(transfer_p95),transfer_max=VALUES(transfer_max),bytes_total=VALUES(bytes_total),slow_count=VALUES(slow_count),slow_ttft_count=VALUES(slow_ttft_count),slow_transfer_count=VALUES(slow_transfer_count)`,
		v.InstanceID, v.BucketAt, v.RequestCount, v.UpstreamCount, v.Status4xx, v.Status5xx, v.Status504, v.RTP50, v.RTP95, v.RTMax, v.UHTP50, v.UHTP95, v.UHTMax, v.TransferP50, v.TransferP95, v.TransferMax, v.BytesTotal, v.SlowCount, v.SlowTTFTCount, v.SlowTransferCount)
	return err
}

func (s Store) InsertNginxSlowSample(v storage.NginxSlowSample) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO nginx_slow_samples (instance_id,occurred_at,path,status,rt,uht,urt,bytes) VALUES (?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE rt=VALUES(rt),uht=VALUES(uht),urt=VALUES(urt),bytes=VALUES(bytes)`, v.InstanceID, v.OccurredAt, v.Path, v.Status, v.RT, v.UHT, v.URT, v.Bytes)
	return err
}

func (s Store) QueryNginxTiming(q storage.NginxTimingQuery) ([]storage.NginxTimingBucket, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT instance_id,bucket_at,request_count,upstream_count,status_4xx,status_5xx,status_504,rt_p50,rt_p95,rt_max,uht_p50,uht_p95,uht_max,transfer_p50,transfer_p95,transfer_max,bytes_total,slow_count,slow_ttft_count,slow_transfer_count FROM nginx_timing_1m WHERE instance_id=? AND bucket_at>=? ORDER BY bucket_at`, q.InstanceID, q.Since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.NginxTimingBucket
	for rows.Next() {
		var v storage.NginxTimingBucket
		if err = rows.Scan(&v.InstanceID, &v.BucketAt, &v.RequestCount, &v.UpstreamCount, &v.Status4xx, &v.Status5xx, &v.Status504, &v.RTP50, &v.RTP95, &v.RTMax, &v.UHTP50, &v.UHTP95, &v.UHTMax, &v.TransferP50, &v.TransferP95, &v.TransferMax, &v.BytesTotal, &v.SlowCount, &v.SlowTTFTCount, &v.SlowTransferCount); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s Store) QueryNginxSlowSamples(q storage.NginxSlowSampleQuery) ([]storage.NginxSlowSample, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	rows, err := s.db.QueryContext(context.Background(), `SELECT id,instance_id,occurred_at,path,status,rt,uht,urt,bytes FROM nginx_slow_samples WHERE instance_id=? AND occurred_at>=? ORDER BY occurred_at DESC LIMIT ?`, q.InstanceID, q.Since, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.NginxSlowSample
	for rows.Next() {
		var v storage.NginxSlowSample
		if err = rows.Scan(&v.ID, &v.InstanceID, &v.OccurredAt, &v.Path, &v.Status, &v.RT, &v.UHT, &v.URT, &v.Bytes); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
