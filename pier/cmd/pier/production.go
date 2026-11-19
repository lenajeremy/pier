package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
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
	if fs.NArg() != 1 {
		return errors.New("say which deployment: pier promote DEPLOYMENT (pier deployments lists them)")
	}
	c, project, err := projectFor(*projectFlag)
	if err != nil {
		return err
	}
	var res productionState
	if err := call(c, http.MethodPost, "/api/projects/"+url.PathEscape(project)+"/promote", map[string]string{"id": fs.Arg(0)}, &res); err != nil {
		return err
	}
	fmt.Printf("%s %s serves %s now.\n", tick(), res.Address, res.serving())
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
		return c, "", errors.New("this folder isn't linked to a project; pass --project NAME")
	}
	return c, l.Project, nil
}
