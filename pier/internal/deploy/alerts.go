package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"pier/internal/alerts"
	"pier/internal/store"
)

// Alerter is told about problems and their fixes. alerts.Notifier is one:
// it sends each problem once, and once more when it's fixed.
type Alerter interface {
	Raise(ctx context.Context, e alerts.Event)
	Resolve(ctx context.Context, e alerts.Event)
}

// buildKey is the incident a build belongs to: production's builds share
// one, and each branch's previews have their own.
func buildKey(dep store.Deployment) string {
	if dep.Target == store.TargetProduction {
		return "deploy:" + dep.Project + ":production"
	}
	return "deploy:" + dep.Project + ":preview:" + dep.Branch
}

// reportBuild tells Alerts how a build ended: a failure raises its
// incident, and a build that works resolves it.
func (d *Deployer) reportBuild(ctx context.Context, dep store.Deployment, src Source) {
	if d.Alerts == nil || dep.ID == "" || dep.Status == store.Building {
		return
	}
	prod := dep.Target == store.TargetProduction
	e := alerts.Event{
		Key: buildKey(dep), Preview: !prod, Project: dep.Project, Branch: dep.Branch, Commit: dep.Commit,
		Message: dep.Message, Deployment: dep.ID, Link: "/projects/" + dep.Project + "/deployments/" + dep.ID,
	}
	what := "preview"
	if prod {
		what = "production"
	}
	from := ""
	switch {
	case src.Kind == store.SourceCLI && dep.Branch != "":
		from = " sent by pier deploy from " + dep.Branch
	case src.Kind == store.SourceCLI:
		from = " sent by pier deploy"
	case dep.Branch != "":
		from = " from " + dep.Branch
	}
	if dep.Commit != "" {
		from += " at " + short(dep.Commit)
	}
	if dep.Status == store.Ready {
		e.Kind = alerts.DeployRecovered
		e.Title = dep.Project + ": production deploys work again"
		if !prod {
			e.Title = dep.Project + ": previews of " + orWord(dep.Branch, "pier deploy") + " build again"
		}
		e.Summary = fmt.Sprintf("The %s build of %s%s worked.", what, dep.Project, from)
		d.Alerts.Resolve(ctx, e)
		return
	}
	e.Kind = alerts.DeployFailed
	e.Title = dep.Project + ": production deploy failed"
	if !prod {
		e.Title = dep.Project + ": preview of " + orWord(dep.Branch, "pier deploy") + " failed"
	}
	e.Summary = fmt.Sprintf("The %s build of %s%s failed after %s.", what, dep.Project, from, seconds(time.Duration(dep.BuildMS)*time.Millisecond))
	if prod {
		host := d.ProductionHost(ctx, dep.Project)
		if serving := d.serving(ctx, host); serving != "" {
			e.Summary += fmt.Sprintf(" %s still serves %s.", host, serving)
		} else {
			e.Summary += fmt.Sprintf(" Nothing is live at %s yet.", host)
		}
	}
	e.Lines = d.lastLines(dep)
	d.Alerts.Raise(ctx, e)
}

// lastLines are the end of a build's log, as an alert quotes it: the
// error, and what led to it.
func (d *Deployer) lastLines(dep store.Deployment) []string {
	lines, err := d.ReadLog(dep.ID, 0)
	if err != nil || len(lines) == 0 {
		if dep.Error != "" {
			return []string{dep.Error}
		}
		return nil
	}
	if len(lines) > alerts.MaxLines {
		lines = lines[len(lines)-alerts.MaxLines:]
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l.Tag != "" {
			out = append(out, fmt.Sprintf("%-7s %s", l.Tag, l.Text))
		} else {
			out = append(out, l.Text)
		}
	}
	return out
}

func orWord(s, word string) string {
	if strings.TrimSpace(s) == "" {
		return word
	}
	return s
}
