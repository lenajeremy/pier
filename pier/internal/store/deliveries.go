package store

import (
	"context"
	"time"
)

// Delivery states.
const (
	DeliveryPending   = "pending"   // waiting for its first or next try
	DeliveryDelivered = "delivered" // it went out
	DeliveryFailed    = "failed"    // its tries ran out, or a failure was final
	DeliveryDropped   = "dropped"   // too old to send by the time pierd started
)

// AlertDelivery is one alert on its way through one channel, or to one
// person by email.
type AlertDelivery struct {
	ID        int64
	Kind      string // the channel: email, chat or webhook
	Recipient string // an email address, for email
	Event     string // the alert, as JSON
	CreatedAt time.Time
	Attempts  int
	NextAt    time.Time
	State     string
	LastError string
	DoneAt    time.Time
}

// QueueAlert keeps a delivery that's about to be tried, and returns its ID.
func (s *Store) QueueAlert(ctx context.Context, d AlertDelivery) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO alert_deliveries(kind, recipient, event, created_at, next_at) VALUES(?, ?, ?, ?, ?)`,
		d.Kind, d.Recipient, d.Event, d.CreatedAt.UnixMilli(), d.NextAt.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// PendingAlerts lists the deliveries still to be tried, oldest first.
func (s *Store) PendingAlerts(ctx context.Context) ([]AlertDelivery, error) {
	return s.deliveries(ctx, `WHERE state = ?`, DeliveryPending)
}

// AlertDeliveries lists every delivery kept, oldest first, whatever its state.
func (s *Store) AlertDeliveries(ctx context.Context) ([]AlertDelivery, error) {
	return s.deliveries(ctx, ``)
}

func (s *Store) deliveries(ctx context.Context, where string, args ...any) ([]AlertDelivery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, recipient, event, created_at, attempts, next_at, state, last_error, done_at
		FROM alert_deliveries `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertDelivery
	for rows.Next() {
		var d AlertDelivery
		var created, next, done int64
		if err := rows.Scan(&d.ID, &d.Kind, &d.Recipient, &d.Event, &created, &d.Attempts, &next, &d.State, &d.LastError, &done); err != nil {
			return nil, err
		}
		d.CreatedAt, d.NextAt, d.DoneAt = time.UnixMilli(created), time.UnixMilli(next), fromMS(done)
		out = append(out, d)
	}
	return out, rows.Err()
}

// RetryAlert records a try that failed and when the next one is due.
func (s *Store) RetryAlert(ctx context.Context, id int64, attempts int, next time.Time, lastError string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE alert_deliveries SET attempts = ?, next_at = ?, last_error = ? WHERE id = ? AND state = ?`,
		attempts, next.UnixMilli(), lastError, id, DeliveryPending)
	return err
}

// FinishAlert records how a delivery ended: delivered or failed.
func (s *Store) FinishAlert(ctx context.Context, id int64, state string, attempts int, lastError string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE alert_deliveries SET state = ?, attempts = ?, last_error = ?, done_at = ? WHERE id = ?`,
		state, attempts, lastError, time.Now().UnixMilli(), id)
	return err
}

// DropStaleAlerts gives up on the deliveries still pending that were queued
// before a time, saying why, and returns how many there were.
func (s *Store) DropStaleAlerts(ctx context.Context, before time.Time, why string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE alert_deliveries SET state = ?, last_error = ?, done_at = ? WHERE state = ? AND created_at < ?`,
		DeliveryDropped, why, time.Now().UnixMilli(), DeliveryPending, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneAlerts clears deliveries that finished before a time.
func (s *Store) PruneAlerts(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM alert_deliveries WHERE state != ? AND done_at < ?`, DeliveryPending, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
