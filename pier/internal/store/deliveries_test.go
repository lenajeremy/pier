package store

import (
	"context"
	"testing"
	"time"
)

// Done deliveries are cleared once old; one still waiting never is.
func TestOldDeliveriesArePruned(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Now()
	var ids []int64
	for range 3 {
		id, err := s.QueueAlert(ctx, AlertDelivery{Kind: "webhook", Event: "{}", CreatedAt: now, NextAt: now})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	s.FinishAlert(ctx, ids[0], DeliveryDelivered, 1, "")
	s.FinishAlert(ctx, ids[1], DeliveryFailed, 4, "the receiver answered 500")
	s.db.Exec(`UPDATE alert_deliveries SET done_at = ? WHERE id IN (?, ?)`, now.Add(-4*24*time.Hour).UnixMilli(), ids[0], ids[1])
	if n, err := s.PruneAlerts(ctx, now.Add(-3*24*time.Hour)); err != nil || n != 2 {
		t.Fatalf("pruned %d (%v)", n, err)
	}
	left, _ := s.AlertDeliveries(ctx)
	if len(left) != 1 || left[0].ID != ids[2] || left[0].State != DeliveryPending {
		t.Fatalf("left %+v", left)
	}
}
