package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SettingPreviewAlerts is "off" when failed preview builds send no alert.
const SettingPreviewAlerts = "alerts_previews"

// AlertChannel is one place alerts go: email, a Slack or Discord channel,
// or a webhook. Config holds what isn't secret, as JSON; Secret holds the
// rest, sealed, and never leaves pierd.
type AlertChannel struct {
	Kind        string
	Config      string
	Secret      string
	UpdatedAt   time.Time
	LastSent    time.Time // when an alert last went out through it
	LastError   string    // why the last one didn't, if it didn't
	LastErrorAt time.Time
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

// AlertChannels lists the channels that are set up, by kind.
func (s *Store) AlertChannels(ctx context.Context) (map[string]AlertChannel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, config, secret, updated_at, last_sent, last_error, last_error_at FROM alert_channels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]AlertChannel{}
	for rows.Next() {
		var c AlertChannel
		var updated, sent, errAt int64
		if err := rows.Scan(&c.Kind, &c.Config, &c.Secret, &updated, &sent, &c.LastError, &errAt); err != nil {
			return nil, err
		}
		c.UpdatedAt, c.LastSent, c.LastErrorAt = fromMS(updated), fromMS(sent), fromMS(errAt)
		out[c.Kind] = c
	}
	return out, rows.Err()
}

// SetAlertChannel saves a channel's settings. What it last delivered stays.
func (s *Store) SetAlertChannel(ctx context.Context, c AlertChannel) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO alert_channels(kind, config, secret, updated_at) VALUES(?, ?, ?, ?)
		ON CONFLICT(kind) DO UPDATE SET config = excluded.config, secret = excluded.secret, updated_at = excluded.updated_at`,
		c.Kind, c.Config, c.Secret, time.Now().UnixMilli())
	return err
}

// DeleteAlertChannel stops alerts going to a channel. It reports whether
// there was one.
func (s *Store) DeleteAlertChannel(ctx context.Context, kind string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM alert_channels WHERE kind = ?`, kind)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RecordAlertDelivery notes how an alert's delivery through a channel went:
// sent, or the error that stopped it.
func (s *Store) RecordAlertDelivery(ctx context.Context, kind string, failure error) error {
	now := time.Now().UnixMilli()
	var err error
	if failure == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE alert_channels SET last_sent = ?, last_error = '', last_error_at = 0 WHERE kind = ?`, now, kind)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE alert_channels SET last_error = ?, last_error_at = ? WHERE kind = ?`, failure.Error(), now, kind)
	}
	return err
}

// OpenIncident records that a problem happened now. It reports true when
// that starts an incident, which is when an alert goes out: there was none
// open for key, or the open one had gone quiet for longer than quiet. While
// it stays open, the same problem again only counts.
func (s *Store) OpenIncident(ctx context.Context, key, project string, now time.Time, quiet time.Duration) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var last int64
	err = tx.QueryRowContext(ctx, `SELECT last_at FROM alert_incidents WHERE key = ?`, key).Scan(&last)
	opened := errors.Is(err, sql.ErrNoRows) || (err == nil && now.Sub(time.UnixMilli(last)) > quiet)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if opened {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO alert_incidents(key, project, opened_at, last_at, count) VALUES(?, ?, ?, ?, 1)
			ON CONFLICT(key) DO UPDATE SET opened_at = excluded.opened_at, last_at = excluded.last_at, count = 1`,
			key, project, now.UnixMilli(), now.UnixMilli())
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE alert_incidents SET last_at = ?, count = count + 1 WHERE key = ?`, now.UnixMilli(), key)
	}
	if err != nil {
		return false, err
	}
	return opened, tx.Commit()
}

// CloseIncident ends an incident once its problem is fixed. It reports when
// the incident began, or a zero time when none was open, so a recovery is
// only sent for what an alert went out about.
func (s *Store) CloseIncident(ctx context.Context, key string) (time.Time, error) {
	var opened int64
	err := s.db.QueryRowContext(ctx, `DELETE FROM alert_incidents WHERE key = ? RETURNING opened_at`, key).Scan(&opened)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return fromMS(opened), err
}

// AlertEmails reports whether a user gets alerts by email. Everyone does
// until they turn it off.
func (s *Store) AlertEmails(ctx context.Context, userID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_email_off WHERE user_id = ?`, userID).Scan(&n)
	return n == 0, err
}

// SetAlertEmails turns a user's alert emails on or off.
func (s *Store) SetAlertEmails(ctx context.Context, userID int64, on bool) error {
	var err error
	if on {
		_, err = s.db.ExecContext(ctx, `DELETE FROM alert_email_off WHERE user_id = ?`, userID)
	} else {
		_, err = s.db.ExecContext(ctx, `INSERT INTO alert_email_off(user_id) VALUES(?) ON CONFLICT DO NOTHING`, userID)
	}
	return err
}

// Recipient is someone alert emails go to.
type Recipient struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// AlertRecipients lists the people on this server who get alert emails,
// oldest account first.
func (s *Store) AlertRecipients(ctx context.Context) ([]Recipient, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, email FROM users
		WHERE id NOT IN (SELECT user_id FROM alert_email_off) ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recipient{}
	for rows.Next() {
		var r Recipient
		if err := rows.Scan(&r.Name, &r.Email); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
