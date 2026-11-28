package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const envHelp = `A project's own variables. Builds and the running app get them; values are
sealed on the server, and nobody sees them again but members who ask.

  pier env [PROJECT]                          their names and where they reach
  pier env set NAME=VALUE... [--project P]    set them (--env production, preview or
                                              both with a comma; both by default)
  pier env rm NAME... [--project P]           remove them (--env as above)

A deploy gets a change when it next builds: pier deploy, or Redeploy.
Databases and buckets add their own variables; you don't set those here.
PROJECT defaults to the one this folder is linked to.
`

// envCmd is pier env and its subcommands.
func envCmd(args []string) error {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "set", "rm":
			sub, args = args[0], args[1:]
		case "help":
			fmt.Print(envHelp)
			return nil
		}
	}
	fs := flag.NewFlagSet("env", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, envHelp) }
	projectFlag := fs.String("project", "", "the project (default: this folder's)")
	envs := fs.String("env", "production,preview", "where they reach: production, preview, or both with a comma")
	fs.Parse(reorder(args, map[string]bool{"project": true, "env": true}))
	scopes, err := envList(*envs)
	if err != nil || len(scopes) == 0 {
		return errors.New("--env is production, preview, or both with a comma")
	}
	names := fs.Args()
	if sub == "" {
		if len(names) > 1 {
			return errors.New("say one project")
		}
		if len(names) == 1 {
			*projectFlag = names[0]
		}
	} else if len(names) == 0 {
		fmt.Fprint(os.Stderr, envHelp)
		return errors.New("missing variables")
	}
	c, project, err := projectFor(*projectFlag)
	if err != nil {
		return err
	}
	base := "/api/projects/" + url.PathEscape(project) + "/variables"
	switch sub {
	case "set":
		for _, kv := range names {
			name, value, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("%s has no value: write NAME=VALUE", kv)
			}
			if err := call(c, http.MethodPut, base, map[string]any{"name": name, "value": value, "scopes": scopes}, nil); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			fmt.Printf("%s Set %s for %s.\n", tick(), name, strings.Join(scopes, " and "))
		}
		fmt.Println("  Deploys get it from their next build.")
		return nil
	case "rm":
		for _, name := range names {
			if err := call(c, http.MethodPost, base+"/delete", map[string]any{"name": name, "scopes": scopes}, nil); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			fmt.Printf("%s Removed %s from %s.\n", tick(), name, strings.Join(scopes, " and "))
		}
		return nil
	}
	var r struct {
		Variables []struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		} `json:"variables"`
		Inherited []struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		} `json:"inherited"`
	}
	if err := call(c, http.MethodGet, base, nil, &r); err != nil {
		return err
	}
	if len(r.Variables) == 0 && len(r.Inherited) == 0 {
		fmt.Printf("%s has no variables of its own. Set one: pier env set NAME=VALUE\n", project)
		return nil
	}
	for _, v := range r.Variables {
		fmt.Printf("  %-32s %s\n", v.Name, strings.Join(v.Scopes, ", "))
	}
	for _, v := range r.Inherited {
		fmt.Printf("  %-32s %s (shared)\n", v.Name, strings.Join(v.Scopes, ", "))
	}
	return nil
}
