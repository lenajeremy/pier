package alerts

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"pier/internal/secrets"
	"pier/internal/store"
)

// openAt opens a notifier on the database and key in dir, as pierd does on
// each start.
func openAt(t *testing.T, dir string, retries []time.Duration) *Notifier {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Load(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	return &Notifier{
		Store: st, Box: box, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Dashboard: "https://pier.example.com", Server: "pier.example.com", Retries: retries,
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// An alert waiting for its next try when pierd stops goes out when pierd
// starts again, and exactly once.
func TestAlertWaitingWhenPierdStopsGoesOutAfterwards(t *testing.T) {
	dir := t.TempDir()
	// The receiver is down: nothing listens at its address yet.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	n := openAt(t, dir, []time.Duration{time.Second})
	if err := n.Save(context.Background(), Webhook, struct{}{}, map[string]string{secretURL: "http://" + addr + "/hook", secretSigning: NewSigningSecret()}); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	n.handle(ctx, request{e: failed("deploy:blog:production", time.Now())})
	waitFor(t, "the first try to fail", func() bool {
		ds, _ := n.Store.AlertDeliveries(context.Background())
		return len(ds) == 1 && ds[0].Attempts == 1
	})
	// pierd stops before the next try.
	stop()
	n.Wait()
	if ds, _ := n.Store.AlertDeliveries(context.Background()); ds[0].State != store.DeliveryPending || ds[0].LastError == "" {
		t.Fatalf("after stopping, the delivery is %+v", ds[0])
	}
	n.Store.Close()

	// The receiver comes up, and pierd starts again.
	var got atomic.Int32
	l, err = net.Listen("tcp", addr)
	if err != nil {
		t.Skip("the receiver's port was taken in between:", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got.Add(1) })}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	n = openAt(t, dir, []time.Duration{time.Second})
	t.Cleanup(func() { n.Store.Close() })
	ctx, stop = context.WithCancel(context.Background())
	go n.Run(ctx)
	waitFor(t, "the alert to arrive", func() bool { return got.Load() > 0 })
	time.Sleep(300 * time.Millisecond)
	stop()
	n.Wait()
	ds, _ := n.Store.AlertDeliveries(context.Background())
	if got.Load() != 1 || len(ds) != 1 || ds[0].State != store.DeliveryDelivered || ds[0].Attempts != 2 {
		t.Fatalf("arrived %d times; deliveries %+v", got.Load(), ds)
	}
}

// A delivery still waiting from more than a day before is dropped when
// pierd starts, not sent a day late, and the drop is in the activity.
func TestStaleAlertIsDroppedOnStart(t *testing.T) {
	n := newNotifier(t)
	h := newHook(t)
	n.webhookTo(t, h)
	ctx := context.Background()
	old := time.Now().Add(-25 * time.Hour)
	if _, err := n.Store.QueueAlert(ctx, store.AlertDelivery{Kind: Webhook, Event: `{"event":"deploy.failed","title":"old"}`, CreatedAt: old, NextAt: old}); err != nil {
		t.Fatal(err)
	}
	n.resume(ctx)
	n.Wait()
	if len(h.events(t)) != 0 {
		t.Fatalf("a day-old alert was sent: %v", h.events(t))
	}
	ds, _ := n.Store.AlertDeliveries(ctx)
	if len(ds) != 1 || ds[0].State != store.DeliveryDropped || ds[0].LastError == "" {
		t.Fatalf("the old delivery is %+v", ds)
	}
	events, _ := n.Store.Events(ctx, "", 0)
	if len(events) == 0 || events[0].Title != "Dropped old alerts" {
		t.Fatalf("the activity doesn't say: %+v", events)
	}
}

// Each person gets their own email delivery, so a retry never sends a second
// copy to someone who already has one.
func TestEmailDeliveriesArePerPerson(t *testing.T) {
	n := newNotifier(t)
	ctx := context.Background()
	for i, who := range []string{"owner@example.com", "member@example.com"} {
		id, _ := n.Store.CreateUser(ctx, who, "", "x")
		if i > 0 {
			n.Store.SetRole(ctx, id, store.RoleMember)
		}
	}
	// A mail server that refuses every connection: each try fails, finally.
	if err := n.Save(ctx, Email, EmailSettings{Provider: SMTP, Host: "127.0.0.1", Port: 1, From: "alerts@example.com"}, nil); err != nil {
		t.Fatal(err)
	}
	n.raise(failed("deploy:blog:production", time.Now()))
	ds, _ := n.Store.AlertDeliveries(ctx)
	if len(ds) != 2 || ds[0].Recipient != "owner@example.com" || ds[1].Recipient != "member@example.com" {
		t.Fatalf("deliveries %+v", ds)
	}
}
