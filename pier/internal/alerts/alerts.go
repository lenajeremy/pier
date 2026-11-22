// Package alerts tells the owner when something on the server goes wrong,
// and again when it's fixed: by email, in a Slack or Discord channel, or
// with a signed webhook. Each problem is an incident, sent once however
// often it happens again, and once more when it recovers. Alerts are sent in
// the background, with retries, so nothing waits for them. Each delivery is
// kept in SQLite until it's done, so pierd stopping between tries loses
// nothing: it carries on when pierd starts again.
package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"pier/internal/secrets"
	"pier/internal/store"
)

// Event kinds, as a webhook's receiver sees them.
const (
	DeployFailed    = "deploy.failed"
	DeployRecovered = "deploy.recovered"
	HealthFailing   = "health.failing"
	HealthRecovered = "health.recovered"
	JobFailed       = "job.failed"
	JobRecovered    = "job.recovered"
	TestAlert       = "test"
)

// Event is one alert: what happened, in words, and where to look.
type Event struct {
	Kind       string    `json:"event"`
	Key        string    `json:"-"` // the incident it belongs to
	Preview    bool      `json:"-"` // about a preview's build, which the owner can silence
	Project    string    `json:"project,omitempty"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary"`
	Branch     string    `json:"branch,omitempty"`
	Commit     string    `json:"commit,omitempty"`
	Message    string    `json:"message,omitempty"` // the commit's message
	Deployment string    `json:"deployment,omitempty"`
	Job        string    `json:"job,omitempty"`
	Lines      []string  `json:"lines,omitempty"` // the last lines of the error
	Link       string    `json:"url,omitempty"`   // a dashboard path; made absolute when sent
	At         time.Time `json:"at"`
}

// Recovered reports whether the event says a problem is fixed.
func (e Event) Recovered() bool { return strings.HasSuffix(e.Kind, ".recovered") }

// Channel kinds.
const (
	Email   = "email"
	Chat    = "chat" // Slack or Discord
	Webhook = "webhook"
)

// Kinds lists the channels in the order the dashboard shows them.
var Kinds = []string{Email, Chat, Webhook}

// Notifier decides which events become alerts and sends them.
type Notifier struct {
	Store     *store.Store
	Box       *secrets.Box
	Log       *slog.Logger
	Dashboard string       // the dashboard's own URL, which links start from
	Server    string       // what the server is called in messages, like pier.example.com
	Client    *http.Client // for Slack, Discord, webhooks and Resend; nil is a default
	ResendURL string       // empty is Resend's own API
	// At most Limit alerts go out in any Window, however many problems
	// there are; the rest are counted and said in the next one.
	Limit  int
	Window time.Duration
	// Quiet is how long an open incident can go without happening again
	// before the next time counts as a new one. A branch that failed and was
	// then left alone shouldn't stay silenced for good.
	Quiet time.Duration
	// Retries are the waits before each new try of a delivery that failed.
	Retries []time.Duration

	start   sync.Once
	queue   chan request
	mu      sync.Mutex
	sent    []time.Time // when alerts went out, within Window
	held    int         // alerts the limit held back since the last one sent
	pending sync.WaitGroup
}

type request struct {
	e       Event
	resolve bool
}

// Defaults for a Notifier's unset fields.
const (
	DefaultLimit  = 20
	DefaultWindow = 10 * time.Minute
	DefaultQuiet  = 24 * time.Hour
)

var defaultRetries = []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute}

func (n *Notifier) init() {
	n.start.Do(func() {
		n.queue = make(chan request, 256)
		if n.Limit == 0 {
			n.Limit = DefaultLimit
		}
		if n.Window == 0 {
			n.Window = DefaultWindow
		}
		if n.Quiet == 0 {
			n.Quiet = DefaultQuiet
		}
		if n.Retries == nil {
			n.Retries = defaultRetries
		}
		if n.Client == nil {
			n.Client = &http.Client{Timeout: 15 * time.Second}
		}
		// A webhook that redirects could send the alert, and Slack's or
		// Discord's secret URL, somewhere else.
		if n.Client.CheckRedirect == nil {
			c := *n.Client
			c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			n.Client = &c
		}
	})
}

// Limits says how many alerts may go out in how long.
func (n *Notifier) Limits() (int, time.Duration) {
	n.init()
	return n.Limit, n.Window
}

// Raise reports a problem. The first time it happens an alert goes out;
// while its incident stays open, it happens again in silence.
func (n *Notifier) Raise(_ context.Context, e Event) { n.enqueue(request{e: e}) }

// Resolve reports that a problem is fixed. A recovery goes out only for an
// incident an alert was sent about.
func (n *Notifier) Resolve(_ context.Context, e Event) { n.enqueue(request{e: e, resolve: true}) }

// enqueue never waits: a deploy that fails mustn't be held up by its alert.
func (n *Notifier) enqueue(r request) {
	if n == nil {
		return
	}
	n.init()
	if r.e.At.IsZero() {
		r.e.At = time.Now()
	}
	select {
	case n.queue <- r:
	default:
		n.Log.Warn("alert queue full; dropped an alert", "title", r.e.Title)
	}
}

// Run picks up the deliveries pierd stopped in the middle of, then handles
// events one at a time, in the order they came, until ctx ends. Deliveries
// carry on in the background.
func (n *Notifier) Run(ctx context.Context) {
	n.init()
	n.resume(ctx)
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-n.queue:
			n.handle(ctx, r)
		case <-prune.C:
			n.prune(ctx)
		}
	}
}

// StaleAfter is how old a delivery still waiting when pierd starts can be
// and still go out. An older one is dropped: an alert a day late says
// something that's long out of date.
const StaleAfter = 24 * time.Hour

// KeepDeliveries is how long a delivery that's done is kept.
const KeepDeliveries = 3 * 24 * time.Hour

// resume carries on with the deliveries still waiting, each from its next
// try, after dropping the ones too old to send. A drop is recorded in the
// delivery and in the activity, so it's not silent.
func (n *Notifier) resume(ctx context.Context) {
	dropped, err := n.Store.DropStaleAlerts(ctx, time.Now().Add(-StaleAfter), "It was more than a day old when pierd started, so it wasn't sent late.")
	if err != nil {
		n.Log.Warn("drop old alerts", "err", err)
	}
	if dropped > 0 {
		n.Log.Warn("dropped alerts that were more than a day old", "count", dropped)
		if err := n.Store.RecordEvent(ctx, "", "Dropped old alerts", fmt.Sprintf("%s more than a day old when pierd started, so they weren't sent late", plural(int(dropped), "delivery")), "", ""); err != nil {
			n.Log.Warn("record activity", "err", err)
		}
	}
	waiting, err := n.Store.PendingAlerts(ctx)
	if err != nil {
		n.Log.Warn("read the alerts waiting to go out", "err", err)
		return
	}
	for _, d := range waiting {
		n.begin(ctx, d)
	}
	n.prune(ctx)
}

func (n *Notifier) prune(ctx context.Context) {
	if _, err := n.Store.PruneAlerts(ctx, time.Now().Add(-KeepDeliveries)); err != nil {
		n.Log.Warn("clear old alert deliveries", "err", err)
	}
}

// Wait waits for the deliveries under way, retries included.
func (n *Notifier) Wait() { n.pending.Wait() }

func (n *Notifier) handle(ctx context.Context, r request) {
	n.init()
	e := r.e
	if e.Preview {
		if v, _ := n.Store.Setting(ctx, store.SettingPreviewAlerts); v == "off" {
			if r.resolve {
				n.Store.CloseIncident(ctx, e.Key)
			}
			return
		}
	}
	if r.resolve {
		opened, err := n.Store.CloseIncident(ctx, e.Key)
		if err != nil {
			n.Log.Warn("close an alert's incident", "key", e.Key, "err", err)
			return
		}
		if opened.IsZero() {
			return
		}
		e.Summary = strings.TrimSpace(e.Summary + " The first alert about it was sent " + Ago(e.At.Sub(opened)) + ".")
	} else if e.Key != "" {
		opened, err := n.Store.OpenIncident(ctx, e.Key, e.Project, e.At, n.Quiet)
		if err != nil {
			n.Log.Warn("record an alert's incident", "key", e.Key, "err", err)
			return
		}
		if !opened {
			return
		}
	}
	held, ok := n.allow(e.At)
	if !ok {
		n.Log.Warn("too many alerts; holding one back", "title", e.Title, "limit", n.Limit, "window", n.Window)
		return
	}
	if held > 0 {
		e.Summary = strings.TrimSpace(fmt.Sprintf("%s Pier also held back %s in the last %s, so as not to flood you; the dashboard shows what's wrong.",
			e.Summary, plural(held, "other alert"), Span(n.Window)))
	}
	n.send(ctx, e)
}

// allow counts an alert against the limit. It reports how many were held
// back since the last one that went out, and whether this one may go.
func (n *Notifier) allow(now time.Time) (int, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	keep := n.sent[:0]
	for _, t := range n.sent {
		if now.Sub(t) < n.Window {
			keep = append(keep, t)
		}
	}
	n.sent = keep
	if len(n.sent) >= n.Limit {
		n.held++
		return 0, false
	}
	n.sent = append(n.sent, now)
	held := n.held
	n.held = 0
	return held, true
}

// send queues an event for every channel that's set up, once per channel,
// and once per person for email, and starts each delivery in the
// background with its own retries.
func (n *Notifier) send(ctx context.Context, e Event) {
	chans, err := n.channels(ctx)
	if err != nil {
		n.Log.Warn("read alert channels", "err", err)
		return
	}
	body, err := json.Marshal(e)
	if err != nil {
		n.Log.Warn("keep an alert", "err", err)
		return
	}
	now := time.Now()
	for _, c := range chans {
		to := []string{""}
		if c.kind == Email {
			people, err := n.Store.AlertRecipients(ctx)
			if err != nil {
				n.Log.Warn("read who gets alert emails", "err", err)
				continue
			}
			if len(people) == 0 {
				n.recordDelivery(ctx, Email, permanentError{errors.New("nobody on this server has alert emails on")})
				continue
			}
			to = to[:0]
			for _, p := range people {
				to = append(to, p.Email)
			}
		}
		for _, rcpt := range to {
			d := store.AlertDelivery{Kind: c.kind, Recipient: rcpt, Event: string(body), CreatedAt: now, NextAt: now}
			if d.ID, err = n.Store.QueueAlert(ctx, d); err != nil {
				n.Log.Warn("keep an alert", "channel", c.kind, "err", err)
				continue
			}
			n.begin(ctx, d)
		}
	}
}

// begin tries a delivery in the background.
func (n *Notifier) begin(ctx context.Context, d store.AlertDelivery) {
	n.pending.Add(1)
	go func() {
		defer n.pending.Done()
		n.deliver(ctx, d)
	}()
}

// deliver tries a delivery until it goes, a failure is final, or the
// retries run out, waiting Retries between tries. Each try is recorded, so
// when ctx ends (pierd is stopping) the delivery stays waiting, and the next
// start carries on from its next try.
func (n *Notifier) deliver(ctx context.Context, d store.AlertDelivery) {
	bg := context.WithoutCancel(ctx)
	var e Event
	if err := json.Unmarshal([]byte(d.Event), &e); err != nil {
		n.Store.FinishAlert(bg, d.ID, store.DeliveryFailed, d.Attempts, "the alert couldn't be read: "+err.Error())
		return
	}
	for {
		if wait := time.Until(d.NextAt); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		if ctx.Err() != nil {
			return
		}
		err := n.attempt(ctx, d, e)
		if err != nil && ctx.Err() != nil {
			return // cut short by pierd stopping: this try didn't count
		}
		d.Attempts++
		if err != nil && !permanent(err) && d.Attempts <= len(n.Retries) {
			d.NextAt = time.Now().Add(n.Retries[d.Attempts-1])
			if rerr := n.Store.RetryAlert(bg, d.ID, d.Attempts, d.NextAt, err.Error()); rerr != nil {
				n.Log.Warn("record an alert's try", "err", rerr)
			}
			n.Log.Info("alert delivery failed; trying again", "channel", d.Kind, "in", n.Retries[d.Attempts-1], "err", err)
			continue
		}
		state, msg := store.DeliveryDelivered, ""
		if err != nil {
			state, msg = store.DeliveryFailed, err.Error()
			n.Log.Warn("alert not delivered", "channel", d.Kind, "title", e.Title, "err", err)
		}
		if rerr := n.Store.FinishAlert(bg, d.ID, state, d.Attempts, msg); rerr != nil {
			n.Log.Warn("record an alert's delivery", "err", rerr)
		}
		n.recordDelivery(bg, d.Kind, err)
		return
	}
}

// attempt is one try of a delivery, through its channel as it's set up now.
func (n *Notifier) attempt(ctx context.Context, d store.AlertDelivery, e Event) error {
	chans, err := n.channels(ctx)
	if err != nil {
		return err
	}
	for _, c := range chans {
		if c.kind == d.Kind {
			return n.sendTo(ctx, c, e, d.Recipient)
		}
	}
	return permanentError{errors.New("the channel was removed before the alert went out")}
}

// recordDelivery notes on the channel when an alert last went out, or why
// it didn't, for the Alerts page.
func (n *Notifier) recordDelivery(ctx context.Context, kind string, err error) {
	if rerr := n.Store.RecordAlertDelivery(ctx, kind, err); rerr != nil {
		n.Log.Warn("record an alert's delivery", "err", rerr)
	}
}

// Test sends a test alert through one channel at once, and says exactly
// what went wrong if it didn't arrive.
func (n *Notifier) Test(ctx context.Context, kind string) error {
	n.init()
	chans, err := n.channels(ctx)
	if err != nil {
		return err
	}
	for _, c := range chans {
		if c.kind != kind {
			continue
		}
		e := Event{
			Kind: TestAlert, At: time.Now(), Title: "Test alert from Pier",
			Summary: "Alerts from " + n.Server + " reach you here. Pier sends one when a deploy fails, when production stops passing its health check, and when a scheduled job fails, and another when each is fixed.",
			Link:    "/alerts",
		}
		err := n.sendTo(ctx, c, e, "")
		n.recordDelivery(context.WithoutCancel(ctx), kind, err)
		return err
	}
	return ErrNotSetUp
}

// ErrNotSetUp means there's no channel of that kind to send through.
var ErrNotSetUp = errors.New("that channel isn't set up")

// link makes a dashboard path absolute.
func (n *Notifier) link(path string) string {
	if path == "" || n.Dashboard == "" {
		return ""
	}
	return strings.TrimRight(n.Dashboard, "/") + path
}

// sendTo sends an event through a channel: by email to one person, or to
// everyone who has alert emails on when to is empty.
func (n *Notifier) sendTo(ctx context.Context, c channel, e Event, to string) error {
	e.Link = n.link(e.Link)
	switch c.kind {
	case Email:
		return n.sendEmail(ctx, c, e, to)
	case Chat:
		return n.sendChat(ctx, c, e)
	case Webhook:
		return n.sendWebhook(ctx, c, e)
	}
	return fmt.Errorf("unknown alert channel %q", c.kind)
}

// permanentError is a failure that trying again won't fix, like a webhook
// that answers 404 or a mail server that refuses the sender.
type permanentError struct{ error }

func (p permanentError) Unwrap() error { return p.error }

func permanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// Ago is how long ago something was, in words: "4 minutes ago".
func Ago(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute ago"
	}
	return Span(d) + " ago"
}

// Span is a duration in words, rounded down: "4 minutes", "2 hours".
func Span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return plural(int(d.Seconds()), "second")
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour")
	}
	return plural(int(d.Hours()/24), "day")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// webhookBody is what a webhook receives, and what its signature covers.
func webhookBody(e Event, server string) ([]byte, error) {
	return json.Marshal(struct {
		Event
		Server    string `json:"server"`
		Recovered bool   `json:"recovered"`
	}{e, server, e.Recovered()})
}
