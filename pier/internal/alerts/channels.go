package alerts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"pier/internal/store"
)

// EmailSettings is how alert emails are sent, apart from the password or
// key, which are kept sealed.
type EmailSettings struct {
	Provider string `json:"provider"` // smtp or resend
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	From     string `json:"from"`
}

// Email providers.
const (
	SMTP   = "smtp"
	Resend = "resend"
)

// ChatSettings says which service a channel's webhook URL belongs to.
type ChatSettings struct {
	Service string `json:"service"` // slack or discord
}

// Chat services.
const (
	Slack   = "slack"
	Discord = "discord"
)

// Names of the sealed values, per channel.
const (
	secretPassword = "password" // email over SMTP
	secretAPIKey   = "api_key"  // email through Resend
	secretURL      = "url"      // chat and webhook
	secretSigning  = "signing"  // webhook
)

// channel is one channel as it's sent through, its secrets opened.
type channel struct {
	kind    string
	email   EmailSettings
	chat    ChatSettings
	secrets map[string]string
}

func binding(kind string) string { return "alert\x00" + kind }

// Configured is a channel as the dashboard sees it: its settings and which
// of its secrets are set, never the secrets themselves.
type Configured struct {
	Kind        string         `json:"kind"`
	Email       *EmailSettings `json:"email,omitempty"`
	Chat        *ChatSettings  `json:"chat,omitempty"`
	Set         []string       `json:"set"` // the secrets it has: password, api_key, url, signing
	UpdatedAt   time.Time      `json:"updated_at"`
	LastSent    *time.Time     `json:"last_sent,omitempty"`
	LastError   string         `json:"last_error,omitempty"`
	LastErrorAt *time.Time     `json:"last_error_at,omitempty"`
}

func at(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// Channels lists the channels that are set up, for the dashboard.
func (n *Notifier) Channels(ctx context.Context) (map[string]Configured, error) {
	rows, err := n.Store.AlertChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]Configured{}
	for kind, row := range rows {
		c, err := n.open(row)
		if err != nil {
			return nil, err
		}
		v := Configured{Kind: kind, Set: []string{}, UpdatedAt: row.UpdatedAt, LastSent: at(row.LastSent), LastError: row.LastError, LastErrorAt: at(row.LastErrorAt)}
		for _, name := range []string{secretPassword, secretAPIKey, secretURL, secretSigning} {
			if c.secrets[name] != "" {
				v.Set = append(v.Set, name)
			}
		}
		switch kind {
		case Email:
			v.Email = &c.email
		case Chat:
			v.Chat = &c.chat
		}
		out[kind] = v
	}
	return out, nil
}

func (n *Notifier) channels(ctx context.Context) ([]channel, error) {
	rows, err := n.Store.AlertChannels(ctx)
	if err != nil {
		return nil, err
	}
	var out []channel
	for _, kind := range Kinds {
		if row, ok := rows[kind]; ok {
			c, err := n.open(row)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
	}
	return out, nil
}

func (n *Notifier) open(row store.AlertChannel) (channel, error) {
	c := channel{kind: row.Kind, secrets: map[string]string{}}
	switch row.Kind {
	case Email:
		json.Unmarshal([]byte(row.Config), &c.email)
	case Chat:
		json.Unmarshal([]byte(row.Config), &c.chat)
	}
	if row.Secret != "" {
		raw, err := n.Box.Open(row.Secret, binding(row.Kind))
		if err != nil {
			return c, fmt.Errorf("the %s alert channel's secrets: %w", row.Kind, err)
		}
		if err := json.Unmarshal([]byte(raw), &c.secrets); err != nil {
			return c, fmt.Errorf("the %s alert channel's secrets: %w", row.Kind, err)
		}
	}
	return c, nil
}

// Secrets returns a channel's secrets opened, for keeping the ones a change
// leaves alone. Nothing that reaches the browser may come from here.
func (n *Notifier) Secrets(ctx context.Context, kind string) (map[string]string, error) {
	rows, err := n.Store.AlertChannels(ctx)
	if err != nil {
		return nil, err
	}
	row, ok := rows[kind]
	if !ok {
		return map[string]string{}, nil
	}
	c, err := n.open(row)
	return c.secrets, err
}

// Save stores a channel's settings, sealing its secrets.
func (n *Notifier) Save(ctx context.Context, kind string, settings any, secrets map[string]string) error {
	config, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(secrets)
	if err != nil {
		return err
	}
	sealed, err := n.Box.Seal(string(raw), binding(kind))
	if err != nil {
		return err
	}
	return n.Store.SetAlertChannel(ctx, store.AlertChannel{Kind: kind, Config: string(config), Secret: sealed})
}

// NewSigningSecret makes the secret a webhook's receiver checks
// signatures with.
func NewSigningSecret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return "whsec_" + hex.EncodeToString(b)
}

// SignatureHeader carries a webhook delivery's signature.
const SignatureHeader = "X-Pier-Signature"

// Sign is the signature of a webhook's body: "sha256=" and the hex
// HMAC-SHA256 of the body, keyed with the signing secret.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a signature the way a receiver should, in constant time.
func Verify(secret string, body []byte, signature string) bool {
	return hmac.Equal([]byte(Sign(secret, body)), []byte(signature))
}

// ChatService says whether a webhook URL is Slack's or Discord's.
func ChatService(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("Paste the webhook URL Slack or Discord gave you. It starts with https://.")
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case (host == "hooks.slack.com" || host == "hooks.slack-gov.com") && strings.HasPrefix(u.Path, "/services/"):
		return Slack, nil
	case (host == "discord.com" || host == "discordapp.com" || host == "ptb.discord.com" || host == "canary.discord.com") &&
		strings.HasPrefix(u.Path, "/api/webhooks/"):
		return Discord, nil
	}
	return "", errors.New("That isn't a Slack or Discord webhook URL. Use Webhook for anything else.")
}

// WebhookURL checks a URL Pier is to post alerts to.
func WebhookURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return errors.New("Enter the full URL to post to, starting with https://.")
	}
	return nil
}

func (n *Notifier) sendChat(ctx context.Context, c channel, e Event) error {
	var body any
	name := "Slack"
	if c.chat.Service == Discord {
		body, name = discordMessage(e), "Discord"
	} else {
		body = slackMessage(e)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return n.post(ctx, name, c.secrets[secretURL], raw, nil)
}

func (n *Notifier) sendWebhook(ctx context.Context, c channel, e Event) error {
	raw, err := webhookBody(e, n.Server)
	if err != nil {
		return err
	}
	id := make([]byte, 8)
	rand.Read(id)
	headers := map[string]string{
		"X-Pier-Event":    e.Kind,
		"X-Pier-Delivery": hex.EncodeToString(id),
		SignatureHeader:   Sign(c.secrets[secretSigning], raw),
	}
	return n.post(ctx, "The webhook", c.secrets[secretURL], raw, headers)
}

// post sends JSON and turns any answer but a 2xx into an error that says
// what the other end said.
func (n *Notifier) post(ctx context.Context, who, to string, body []byte, headers map[string]string) error {
	if to == "" {
		return permanentError{errors.New("no URL is set")}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, to, bytes.NewReader(body))
	if err != nil {
		return permanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Pier")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := n.Client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			// The URL is a secret; say what failed without it.
			return fmt.Errorf("%s couldn't be reached: %w", who, uerr.Err)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	said, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	err = fmt.Errorf("%s answered %s", who, resp.Status)
	// Discord and Resend explain in JSON; Slack in a word, like no_service.
	var explained struct{ Message, Error string }
	text := strings.TrimSpace(string(said))
	if json.Unmarshal(said, &explained) == nil && (explained.Message != "" || explained.Error != "") {
		text = explained.Message
		if text == "" {
			text = explained.Error
		}
	}
	if text != "" {
		err = fmt.Errorf("%w: %s", err, oneLine(text, 200))
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusRequestTimeout {
		return permanentError{err}
	}
	return err
}

func oneLine(s string, n int) string {
	return clip(strings.Join(strings.Fields(s), " "), n)
}
