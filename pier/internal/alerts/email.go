package alerts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// EmailAddress checks an address alerts are sent from.
func EmailAddress(s string) (string, error) {
	a, err := mail.ParseAddress(strings.TrimSpace(s))
	if err != nil {
		return "", errors.New("Enter the address alerts come from, like alerts@example.com.")
	}
	if a.Name == "" {
		return a.Address, nil
	}
	// A plain name needs no quotes, and reads better without them.
	for _, r := range a.Name {
		if !(r == ' ' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return a.String(), nil
		}
	}
	return a.Name + " <" + a.Address + ">", nil
}

// sendEmail sends an alert to one person, or, with only empty, to everyone
// who has alert emails on, as a test alert does.
func (n *Notifier) sendEmail(ctx context.Context, c channel, e Event, only string) error {
	to := []string{only}
	if only == "" {
		people, err := n.Store.AlertRecipients(ctx)
		if err != nil {
			return err
		}
		if len(people) == 0 {
			return permanentError{errors.New("nobody on this server has alert emails on")}
		}
		to = to[:0]
		for _, p := range people {
			to = append(to, p.Email)
		}
	}
	text := emailText(e, n.Server)
	if c.email.Provider == Resend {
		return n.sendResend(ctx, c, to, e.Title, text)
	}
	return sendSMTP(ctx, c.email, c.secrets[secretPassword], n.Server, to, e.Title, text)
}

// SendEmail sends one plain message to one address through the email
// channel, from its sender: an invite link, say. ErrNotSetUp means email
// isn't set up.
func (n *Notifier) SendEmail(ctx context.Context, to, subject, text string) error {
	n.init()
	chans, err := n.channels(ctx)
	if err != nil {
		return err
	}
	for _, c := range chans {
		if c.kind != Email {
			continue
		}
		if c.email.Provider == Resend {
			return n.sendResend(ctx, c, []string{to}, subject, text)
		}
		return sendSMTP(ctx, c.email, c.secrets[secretPassword], n.Server, []string{to}, subject, text)
	}
	return ErrNotSetUp
}

// sendSMTP sends one message to each recipient, so nobody sees who else
// gets them. Port 465 speaks TLS from the start; on any other port Pier
// upgrades with STARTTLS when the server offers it, and won't send a
// password over a connection that isn't encrypted, except to this machine.
func sendSMTP(ctx context.Context, s EmailSettings, password, hello string, to []string, subject, text string) error {
	port := s.Port
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(s.Host, strconv.Itoa(port))
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialer := &net.Dialer{Deadline: deadline}
	var conn net.Conn
	var err error
	tlsConfig := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	if port == 465 {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("couldn't connect to %s: %w", addr, err)
	}
	conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return smtpError(err)
	}
	defer c.Close()
	if hello == "" {
		hello = "localhost"
	}
	if h, _, err := net.SplitHostPort(hello); err == nil {
		hello = h
	}
	if err := c.Hello(hello); err != nil {
		return smtpError(err)
	}
	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("STARTTLS with %s failed: %w", s.Host, err)
			}
		} else if s.User != "" && !loopback(s.Host) {
			return permanentError{fmt.Errorf("%s doesn't offer STARTTLS, so Pier won't send the password over an unencrypted connection", s.Host)}
		}
	}
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, password, s.Host)); err != nil {
			return smtpError(err)
		}
	}
	from, _ := mail.ParseAddress(s.From)
	for i, rcpt := range to {
		if i > 0 {
			if err := c.Reset(); err != nil {
				return smtpError(err)
			}
		}
		if err := c.Mail(from.Address); err != nil {
			return smtpError(err)
		}
		if err := c.Rcpt(rcpt); err != nil {
			return smtpError(err)
		}
		w, err := c.Data()
		if err != nil {
			return smtpError(err)
		}
		if _, err := w.Write(message(s.From, rcpt, subject, text)); err != nil {
			return smtpError(err)
		}
		if err := w.Close(); err != nil {
			return smtpError(err)
		}
	}
	return smtpError(c.Quit())
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// smtpError keeps what the mail server said, and marks the refusals that
// sending again won't change.
func smtpError(err error) error {
	if err == nil {
		return nil
	}
	var te *textproto.Error
	if errors.As(err, &te) {
		err = fmt.Errorf("the mail server answered %d %s", te.Code, te.Msg)
		if te.Code >= 500 {
			return permanentError{err}
		}
	}
	return err
}

// message is one email: headers, then the text in quoted-printable, which
// keeps any line under the 78 characters mail servers ask for.
func message(from, to, subject, text string) []byte {
	id := make([]byte, 12)
	rand.Read(id)
	domain := "pier"
	if a, err := mail.ParseAddress(from); err == nil {
		if _, d, ok := strings.Cut(a.Address, "@"); ok {
			domain = d
		}
	}
	var b bytes.Buffer
	for _, h := range [][2]string{
		{"From", from},
		{"To", to},
		{"Subject", mime.QEncoding.Encode("utf-8", subject)},
		{"Date", time.Now().Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=utf-8"},
		{"Content-Transfer-Encoding", "quoted-printable"},
		{"Auto-Submitted", "auto-generated"},
	} {
		b.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	qp.Write([]byte(strings.ReplaceAll(text, "\n", "\r\n")))
	qp.Close()
	return b.Bytes()
}

// sendResend sends through Resend's API, one message per recipient.
func (n *Notifier) sendResend(ctx context.Context, c channel, to []string, subject, text string) error {
	endpoint := n.ResendURL
	if endpoint == "" {
		endpoint = "https://api.resend.com/emails"
	}
	key := c.secrets[secretAPIKey]
	for _, rcpt := range to {
		body, _ := json.Marshal(map[string]any{"from": c.email.From, "to": []string{rcpt}, "subject": subject, "text": text})
		if err := n.post(ctx, "Resend", endpoint, body, map[string]string{"Authorization": "Bearer " + key}); err != nil {
			return err
		}
	}
	return nil
}
