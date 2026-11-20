package store

import (
	"context"
	"time"
)

type ServerSample struct {
	At     int64   `json:"at"`
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"`
	Disk   float64 `json:"disk"`
}

func (s *Store) RecordSample(ctx context.Context, p ServerSample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO server_samples(at,cpu,memory,disk) VALUES(?,?,?,?)", p.At, p.CPU, p.Memory, p.Disk); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM server_samples WHERE at<?", p.At-(7*24*time.Hour).Milliseconds()); err != nil {
		return err
	}
	return tx.Commit()
}

// Samples returns mean usage in occupied buckets. Unobserved intervals are gaps.
func (s *Store) Samples(ctx context.Context, since int64, bucket time.Duration) ([]ServerSample, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at / ? * ? AS bucket,avg(cpu),avg(memory),avg(disk) FROM server_samples WHERE at>=? GROUP BY bucket ORDER BY bucket`, bucket.Milliseconds(), bucket.Milliseconds(), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerSample{}
	for rows.Next() {
		var p ServerSample
		if err := rows.Scan(&p.At, &p.CPU, &p.Memory, &p.Disk); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
