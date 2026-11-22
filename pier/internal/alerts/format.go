package alerts

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxLines is how many of an error's last lines an alert quotes.
const MaxLines = 12

// facts are the event's details that apply, as label and value.
func facts(e Event) [][2]string {
	var out [][2]string
	add := func(label, value string) {
		if value != "" {
			out = append(out, [2]string{label, value})
		}
	}
	add("Project", e.Project)
	add("Job", e.Job)
	add("Branch", e.Branch)
	commit := short(e.Commit)
	if e.Message != "" {
		commit = strings.TrimSpace(commit + " " + clip(e.Message, 120))
	}
	add("Commit", commit)
	add("Deployment", e.Deployment)
	return out
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// clip shortens s to at most n characters, on a character boundary.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// lines are the event's last lines, each kept short.
func lines(e Event) []string {
	ls := e.Lines
	if len(ls) > MaxLines {
		ls = ls[len(ls)-MaxLines:]
	}
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, clip(strings.TrimRight(l, "\r\n"), 240))
	}
	return out
}

// emailText is the plain text of an alert's email.
func emailText(e Event, server string) string {
	var b strings.Builder
	b.WriteString(e.Summary)
	b.WriteString("\n\n")
	if f := facts(e); len(f) > 0 {
		for _, kv := range f {
			fmt.Fprintf(&b, "%-11s %s\n", kv[0], kv[1])
		}
		b.WriteString("\n")
	}
	if ls := lines(e); len(ls) > 0 {
		b.WriteString("The last lines:\n\n")
		for _, l := range ls {
			b.WriteString("    " + l + "\n")
		}
		b.WriteString("\n")
	}
	if e.Link != "" {
		b.WriteString("Open it in Pier: " + e.Link + "\n\n")
	}
	b.WriteString("-- \nYou get this because alert emails are on for your account on " + server +
		". Turn them off in the account menu at the foot of the dashboard's sidebar.\n")
	return b.String()
}

// slackEscape escapes the three characters Slack's mrkdwn treats as markup.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// slackMessage is an alert in Slack's own format: Block Kit, with plain
// text for notifications.
func slackMessage(e Event) map[string]any {
	blocks := []map[string]any{{
		"type": "section",
		"text": map[string]any{"type": "mrkdwn", "text": "*" + slackEscape(e.Title) + "*\n" + slackEscape(e.Summary)},
	}}
	if f := facts(e); len(f) > 0 {
		var fields []map[string]any
		for _, kv := range f {
			fields = append(fields, map[string]any{"type": "mrkdwn", "text": "*" + kv[0] + "*\n" + slackEscape(kv[1])})
		}
		blocks = append(blocks, map[string]any{"type": "section", "fields": fields})
	}
	if ls := lines(e); len(ls) > 0 {
		code := clip(strings.ReplaceAll(strings.Join(ls, "\n"), "```", "'''"), 2900)
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": "```" + slackEscape(code) + "```"},
		})
	}
	if e.Link != "" {
		blocks = append(blocks, map[string]any{
			"type": "actions",
			"elements": []map[string]any{{
				"type": "button", "url": e.Link,
				"text": map[string]any{"type": "plain_text", "text": "Open in Pier"},
			}},
		})
	}
	return map[string]any{"text": e.Title, "blocks": blocks}
}

// Discord's embed colours: the dashboard's red for a problem, teal for a
// fix, and the info blue for a test.
const (
	discordRed  = 0xFF8080
	discordTeal = 0x62D8C6
	discordBlue = 0x8DB2F8
)

// discordMessage is an alert in Discord's own format: one embed, and no
// mentions, so a commit message can't ping a channel.
func discordMessage(e Event) map[string]any {
	color := discordRed
	switch {
	case e.Kind == TestAlert:
		color = discordBlue
	case e.Recovered():
		color = discordTeal
	}
	desc := e.Summary
	if ls := lines(e); len(ls) > 0 {
		desc += "\n```\n" + strings.ReplaceAll(strings.Join(ls, "\n"), "```", "'''") + "\n```"
	}
	embed := map[string]any{
		"title":       clip(e.Title, 256),
		"description": clip(desc, 4000),
		"color":       color,
		"timestamp":   e.At.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if e.Link != "" {
		embed["url"] = e.Link
	}
	var fields []map[string]any
	for _, kv := range facts(e) {
		fields = append(fields, map[string]any{"name": kv[0], "value": clip(kv[1], 1000), "inline": true})
	}
	if len(fields) > 0 {
		embed["fields"] = fields
	}
	return map[string]any{
		"username":         "Pier",
		"embeds":           []any{embed},
		"allowed_mentions": map[string]any{"parse": []string{}},
	}
}
