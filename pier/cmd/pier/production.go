package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// rollback points production back at the deployment before it (or one
// named), or with --undo puts it back where it was.
func rollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	undo := fs.Bool("undo", false, "undo the rollback: production goes back to what it served before")
	projectFlag := fs.String("project", "", "the project (default: this folder's, from .pier/project.json)")
	fs.Parse(reorder(args, map[string]bool{"project": true}))
	c, project, err := projectFor(*projectFlag)
	if err != nil {
		return err
	}
	path := "/api/projects/" + url.PathEscape(project) + "/rollback"
	body := map[string]string{}
	if *undo {
		path += "/undo"
	} else if fs.NArg() > 0 {
		body["id"] = fs.Arg(0)
	} else if interactive() {
		var proj struct {
			RolledBackFrom string `json:"rolled_back_from"`
			RollbackTo     string `json:"rollback_to"`
			Deployments    []deploymentSummary
		}
		if err := call(c, http.MethodGet, "/api/projects/"+url.PathEscape(project), nil, &proj); err != nil {
			return err
		}
		var items []choice
		if proj.RolledBackFrom != "" {
			items = append(items, choice{
				id:      "__undo__",
				title:   "Undo rollback (restore " + proj.RolledBackFrom + ")",
				hint:    "resume automatic deploys",
				icon:    "✦",
				section: "Rollback",
			})
		}
		for _, d := range proj.Deployments {
			if d.Status != "ready" || d.Role == "production" {
				continue
			}
			title := d.ID
			if msg := cleanLog(strings.TrimSpace(d.Message)); msg != "" {
				title += "  " + msg
			}
			hint := ago(d.CreatedAt)
			if d.ID == proj.RollbackTo {
				hint = "previous · " + hint
			}
			items = append(items, choice{
				id:      d.ID,
				title:   title,
				hint:    hint,
				icon:    "←",
				badge:   d.Role,
				section: "Ready deployments",
			})
		}
		if len(items) == 0 {
			return errors.New("no previous ready deployment to roll back to")
		}
		picked, err := selectItem("Roll back production", host(c.Server), project, items)
		if err != nil {
			return err
		}
		if picked == "" {
			return errCancelled
		}
		if picked == "__undo__" {
			*undo = true
			path += "/undo"
		} else {
			body["id"] = picked
		}
	}
	var res productionState
	if err := call(c, http.MethodPost, path, body, &res); err != nil {
		return err
	}
	if *undo {
		fmt.Printf("%s %s serves %s again. Production builds go live on their own again.\n", tick(), res.Address, res.serving())
		return nil
	}
	fmt.Printf("%s %s serves %s now, rolled back from %s.\n", tick(), res.Address, res.serving(), res.RolledBackFrom)
	fmt.Println("  New production builds won't go live until you promote one, or run: pier rollback --undo")
	return nil
}

// promote points production at a deployment that already built.
func promote(args []string) error {
	fs := flag.NewFlagSet("promote", flag.ExitOnError)
	projectFlag := fs.String("project", "", "the project (default: this folder's, from .pier/project.json)")
	fs.Parse(reorder(args, map[string]bool{"project": true}))
	c, project, err := projectFor(*projectFlag)
	if err != nil {
		return err
	}
	id := ""
	if fs.NArg() == 1 {
		id = fs.Arg(0)
	} else if fs.NArg() > 1 {
		return errors.New("say which deployment: pier promote DEPLOYMENT (pier deployments lists them)")
	} else if interactive() {
		var res struct {
			Deployments []deploymentSummary
		}
		if err := call(c, http.MethodGet, "/api/projects/"+url.PathEscape(project)+"/deployments", nil, &res); err != nil {
			return err
		}
		var items []choice
		for _, d := range res.Deployments {
			if d.Status != "ready" || d.Role == "production" {
				continue
			}
			title := d.ID
			if msg := cleanLog(strings.TrimSpace(d.Message)); msg != "" {
				title += "  " + msg
			}
			items = append(items, choice{
				id:    d.ID,
				title: title,
				hint:  ago(d.CreatedAt),
				icon:  "✦",
				badge: d.Role,
			})
		}
		if len(items) == 0 {
			return errors.New("no ready non-production deployment to promote")
		}
		picked, err := selectItem("Promote to production", host(c.Server), project, items)
		if err != nil {
			return err
		}
		if picked == "" {
			return errCancelled
		}
		id = picked
	} else {
		return errors.New("say which deployment: pier promote DEPLOYMENT (pier deployments lists them)")
	}
	var res productionState
	if err := call(c, http.MethodPost, "/api/projects/"+url.PathEscape(project)+"/promote", map[string]string{"id": id}, &res); err != nil {
		return err
	}
	fmt.Printf("%s %s serves %s now.\n", tick(), res.Address, res.serving())
	return nil
}

// restart bounces a ready server deployment's container without rebuilding its image.
func restart(args []string) error {
	fs := flag.NewFlagSet("restart", flag.ExitOnError)
	projectFlag := fs.String("project", "", "the project (default: this folder's, from .pier/project.json)")
	deploymentFlag := fs.String("deployment", "", "deployment ID to restart (defaults to production)")
	fs.Parse(reorder(args, map[string]bool{"project": true, "deployment": true}))
	if fs.NArg() > 1 || (fs.NArg() == 1 && *projectFlag != "") {
		return errors.New("specify one project")
	}
	if fs.NArg() == 1 {
		*projectFlag = fs.Arg(0)
	}
	c, project, err := projectFor(*projectFlag)
	if err != nil {
		return err
	}
	id := *deploymentFlag
	if id == "" {
		var proj struct {
			Production *struct {
				Deployment *struct {
					ID      string `json:"id"`
					Runtime string `json:"runtime"`
				} `json:"deployment"`
			} `json:"production"`
		}
		if err := call(c, http.MethodGet, "/api/projects/"+url.PathEscape(project), nil, &proj); err != nil {
			return err
		}
		if proj.Production == nil || proj.Production.Deployment == nil || proj.Production.Deployment.Runtime == "" {
			return fmt.Errorf("%s has no running server production deployment", project)
		}
		id = proj.Production.Deployment.ID
	}
	path := "/api/projects/" + url.PathEscape(project) + "/deployments/" + url.PathEscape(id) + "/restart"
	if err := call(c, http.MethodPost, path, map[string]string{}, nil); err != nil {
		return err
	}
	fmt.Printf("%s Restarted %s (%s).\n", tick(), project, id)
	return nil
}

type productionState struct {
	Address        string `json:"address"`
	RolledBackFrom string `json:"rolled_back_from"`
	Production     *struct {
		Deployment *struct{ ID string } `json:"deployment"`
	} `json:"production"`
}

func (p productionState) serving() string {
	if p.Production != nil && p.Production.Deployment != nil {
		return p.Production.Deployment.ID
	}
	return "nothing"
}

func projectFor(flagged string) (config, string, error) {
	c, err := signedIn()
	if err != nil {
		return c, "", err
	}
	if flagged != "" {
		return c, flagged, nil
	}
	l, err := readLink(".")
	if err != nil {
		if interactive() {
			picked, perr := projectChoices(c, false)
			if perr != nil {
				return c, "", perr
			}
			if picked == "" {
				return c, "", errCancelled
			}
			return c, picked, nil
		}
		return c, "", errors.New("this folder isn't linked to a project; pass --project NAME")
	}
	return c, l.Project, nil
}
