package promq

import (
	"strings"
	"testing"
)

func TestScope(t *testing.T) {
	for _, c := range []struct{ in, env, want string }{
		{`up`, "", `up{pier_project="shop"}`},
		{`rate(http_requests_total{pier_project="bank", code=~"5.."}[5m])`, "", `rate(http_requests_total{code=~"5..",pier_project="shop"}[5m])`},
		{`sum by (le) (rate(x_bucket[5m])) / on() group_left sum(y)`, "production", `sum by (le) (rate(x_bucket{deployment_environment="production",pier_project="shop"}[5m])) / on () group_left () sum(y{deployment_environment="production",pier_project="shop"})`},
		{`{__name__=~".+"}`, "", `{__name__=~".+",pier_project="shop"}`},
		{`max_over_time(foo[1h:5m])`, "", `max_over_time(foo{pier_project="shop"}[1h:5m])`},
		{`label_replace(foo, "pier_project", "bank", "", "")`, "", `label_replace(foo{pier_project="shop"}, "pier_project", "bank", "", "")`},
	} {
		got, err := Scope(c.in, "shop", c.env)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if strings.ReplaceAll(got, ", ", ",") != strings.ReplaceAll(c.want, ", ", ",") {
			t.Errorf("%s\n got %s\nwant %s", c.in, got, c.want)
		}
	}
	if _, err := Scope(`sum(`, "shop", ""); err == nil {
		t.Fatal("a broken query parsed")
	}
}
