// Package promq keeps a PromQL query to one project's series: it parses the
// query and pins the project's label on every selector in it, replacing
// any the query set, so a query can't read another project's metrics.
package promq

import (
	"fmt"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// ProjectLabel is the label Prometheus gives a series from the resource's
// pier.project attribute.
const ProjectLabel = "pier_project"

// EnvLabel is the label for deployment.environment.
const EnvLabel = "deployment_environment"

// Error is a query that doesn't parse.
type Error struct{ Msg string }

func (e Error) Error() string { return e.Msg }

// Scope returns the query with every selector limited to project, and to
// env when it isn't empty.
func Scope(query, project, env string) (string, error) {
	expr, err := parser.ParseExpr(query)
	if err != nil {
		return "", Error{err.Error()}
	}
	pin := []*labels.Matcher{labels.MustNewMatcher(labels.MatchEqual, ProjectLabel, project)}
	if env != "" {
		pin = append(pin, labels.MustNewMatcher(labels.MatchEqual, EnvLabel, env))
	}
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		vs, ok := node.(*parser.VectorSelector)
		if !ok {
			return nil
		}
		kept := vs.LabelMatchers[:0:0]
		for _, m := range vs.LabelMatchers {
			if m.Name == ProjectLabel || (env != "" && m.Name == EnvLabel) {
				continue
			}
			kept = append(kept, m)
		}
		vs.LabelMatchers = append(kept, pin...)
		return nil
	})
	return expr.String(), nil
}

// Selector is a series selector for the project: a metric, or every metric
// when name is empty.
func Selector(project, name string) string {
	if name == "" {
		return fmt.Sprintf("{%s=%q}", ProjectLabel, project)
	}
	return fmt.Sprintf("{__name__=%q, %s=%q}", name, ProjectLabel, project)
}
