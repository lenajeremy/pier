package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const storageHelp = `A project's buckets, on your Cloudflare R2. Each bucket serves the environments
it's linked to, and its deploys get its variables: <PREFIX>_BUCKET, _ENDPOINT,
_ACCESS_KEY_ID, _SECRET_ACCESS_KEY, _REGION and, if it's public, _PUBLIC_URL. The
STORAGE_ bucket also sets AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY,
AWS_ENDPOINT_URL_S3 and AWS_REGION, so S3 clients work with no setup.

  pier storage [PROJECT]               the project's buckets
  pier storage add NAME [PROJECT]      add one (--public; --env production,preview or
                                       none, both by default; --prefix, STORAGE for the
                                       first)
  pier storage link NAME [PROJECT]     change what it serves (--env, --prefix)
  pier storage env NAME [PROJECT]      print its variables as KEY=value, secret too,
                                       for a .env file
  pier storage ls NAME[/FOLDER] [PROJECT]       list files
  pier storage put FILE NAME[/KEY] [PROJECT]    upload a file (KEY defaults to its name;
                                                one ending in / is a folder)
  pier storage get NAME/KEY [FILE] [--project P] download a file (to its name)
  pier storage rm NAME [PROJECT] --yes          delete a bucket and every file in it

Cloudflare is connected once, by the owner, in the dashboard's Integrations.
PROJECT defaults to the one this folder is linked to.
`

type bucketInfo struct {
	Name         string   `json:"name"`
	Bucket       string   `json:"bucket"`
	Public       bool     `json:"public"`
	PublicURL    string   `json:"public_url"`
	Environments []string `json:"environments"`
	Prefix       string   `json:"prefix"`
	Files        int      `json:"files"`
	Bytes        int64    `json:"bytes"`
	More         bool     `json:"more"`
	CountError   string   `json:"count_error"`
	Variables    []string `json:"variables"`
	AddressReady bool     `json:"address_ready"`
}

type storageInfo struct {
	Cloudflare struct {
		Connected bool   `json:"connected"`
		Domain    string `json:"domain"`
	} `json:"cloudflare"`
	Buckets []bucketInfo `json:"buckets"`
}

func storagePath(project string) string {
	return "/api/projects/" + url.PathEscape(project) + "/storage"
}

// envList reads --env production,preview|none into environments.
func envList(s string) ([]string, error) {
	out := []string{}
	for _, e := range strings.Split(s, ",") {
		switch strings.TrimSpace(e) {
		case "production", "prod":
			out = append(out, "production")
		case "preview", "previews":
			out = append(out, "preview")
		case "", "none":
		default:
			return nil, fmt.Errorf("%s isn't an environment: say production, preview, both with a comma, or none", e)
		}
	}
	return out, nil
}

// storageCmd is pier storage and its subcommands.
func storageCmd(args []string) error {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "add", "link", "env", "ls", "put", "get", "rm":
			sub, args = args[0], args[1:]
		case "help":
			fmt.Print(storageHelp)
			return nil
		}
	}
	fs := flag.NewFlagSet("storage", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, storageHelp) }
	projectFlag := fs.String("project", "", "the project (default: this folder's)")
	public := fs.Bool("public", false, "add: anyone can read its files")
	envs := fs.String("env", "\x00", "add, link: the environments it serves: production, preview, both with a comma, or none")
	prefix := fs.String("prefix", "", "add, link: what its variables start with")
	yes := fs.Bool("yes", false, "rm: don't ask")
	fs.Parse(reorder(args, map[string]bool{"project": true, "env": true, "prefix": true}))
	rest := fs.Args()

	// The positional arguments each subcommand takes before PROJECT.
	need := map[string]int{"": 0, "add": 1, "link": 1, "env": 1, "ls": 1, "put": 2, "get": 1, "rm": 1}[sub]
	if sub == "get" && len(rest) > 1 {
		need = 2 // NAME/KEY FILE; the project comes from --project or the folder
	}
	if len(rest) < need {
		fmt.Fprint(os.Stderr, storageHelp)
		return errors.New("missing arguments")
	}
	pos, rest := rest[:need], rest[need:]
	if len(rest) > 1 || (len(rest) == 1 && *projectFlag != "") {
		return errors.New("say one project")
	}
	if len(rest) == 1 {
		*projectFlag = rest[0]
	}
	c, project, err := projectFor(*projectFlag)
	if err != nil {
		return err
	}
	base := storagePath(project)
	switch sub {
	case "":
		return storageShow(c, project)
	case "add":
		body := map[string]any{"name": pos[0], "public": *public, "prefix": *prefix, "environments": []string{"production", "preview"}}
		if *envs != "\x00" {
			list, err := envList(*envs)
			if err != nil {
				return err
			}
			body["environments"] = list
		}
		var s storageInfo
		if err := callWith("Adding the bucket "+pos[0], c, http.MethodPost, base, body, &s); err != nil {
			return err
		}
		for _, b := range s.Buckets {
			if b.Name == pos[0] {
				fmt.Printf("%s Added %s to %s, as %s.\n", tick(), b.Name, project, b.Bucket)
				fmt.Printf("  Its deploys get %s from their next build.\n", strings.Join(b.Variables, ", "))
				if b.PublicURL != "" {
					fmt.Printf("  Anyone can read its files at %s.\n", b.PublicURL)
				}
			}
		}
		return nil
	case "link":
		body := map[string]any{"prefix": *prefix}
		var current storageInfo
		if err := call(c, http.MethodGet, base, nil, &current); err != nil {
			return err
		}
		for _, b := range current.Buckets {
			if b.Name == pos[0] {
				body["environments"] = b.Environments
			}
		}
		if *envs != "\x00" {
			list, err := envList(*envs)
			if err != nil {
				return err
			}
			body["environments"] = list
		}
		if err := callWith("Changing "+pos[0], c, http.MethodPut, base+"/"+url.PathEscape(pos[0]), body, nil); err != nil {
			return err
		}
		fmt.Printf("%s Saved. Deploys get the change when they next start.\n", tick())
		return nil
	case "env":
		var r struct {
			Variables []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"variables"`
		}
		if err := call(c, http.MethodGet, base+"/"+url.PathEscape(pos[0])+"/variables", nil, &r); err != nil {
			return err
		}
		for _, v := range r.Variables {
			fmt.Printf("%s=%s\n", v.Name, v.Value)
		}
		return nil
	case "ls":
		return storageList(c, base, pos[0])
	case "put":
		return storagePut(c, base, pos[0], pos[1])
	case "get":
		to := ""
		if len(pos) == 2 {
			to = pos[1]
		}
		return storageGet(c, base, pos[0], to)
	case "rm":
		if !*yes {
			if !term() {
				return errors.New("deleting a bucket deletes every file in it; add --yes")
			}
			answer, err := ask(fmt.Sprintf("Delete %s and every file in it? Type its name: ", pos[0]))
			if err != nil || answer != pos[0] {
				return errors.New("not deleted")
			}
		}
		if err := callWith("Emptying and deleting "+pos[0], c, http.MethodDelete, base+"/"+url.PathEscape(pos[0]), nil, nil); err != nil {
			return err
		}
		fmt.Printf("%s Deleted %s.\n", tick(), pos[0])
		return nil
	}
	return nil
}

func storageShow(c config, project string) error {
	var s storageInfo
	if err := callWith("Loading "+project+"'s buckets", c, http.MethodGet, storagePath(project), nil, &s); err != nil {
		return err
	}
	if !s.Cloudflare.Connected {
		fmt.Println("Storage needs Cloudflare. The owner connects it in the dashboard's Integrations.")
		return nil
	}
	if len(s.Buckets) == 0 {
		fmt.Printf("%s has no buckets. Add one: pier storage add files %s\n", project, project)
		return nil
	}
	if term() {
		heading("Storage", project)
	}
	for _, b := range s.Buckets {
		serves := "nothing"
		if len(b.Environments) > 0 {
			serves = strings.Join(b.Environments, " and ")
		}
		access := "private"
		if b.Public {
			access = "public at " + b.PublicURL
			if !b.AddressReady {
				access += " (setting up)"
			}
		}
		files := fmt.Sprintf("%d files, %s", b.Files, human(b.Bytes))
		if b.More {
			files = "more than " + files
		}
		if b.CountError != "" {
			files = b.CountError
		}
		fmt.Printf("  %-14s %s_*  serves %s, %s\n  %-14s %s; %s\n", b.Name, b.Prefix, serves, access, "", b.Bucket, files)
	}
	fmt.Println()
	return nil
}

// splitTarget reads NAME/KEY into the bucket and the key.
func splitTarget(s string) (string, string) {
	name, key, _ := strings.Cut(s, "/")
	return name, key
}

type fileInfo struct {
	Key      string    `json:"key"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Type     string    `json:"type"`
	Download string    `json:"download"`
	URL      string    `json:"url"`
}

func storageList(c config, base, target string) error {
	name, folder := splitTarget(target)
	if folder != "" && !strings.HasSuffix(folder, "/") {
		folder += "/"
	}
	after := ""
	for {
		q := url.Values{"prefix": {folder}, "limit": {"1000"}}
		if after != "" {
			q.Set("after", after)
		}
		var page struct {
			Folders []string   `json:"folders"`
			Files   []fileInfo `json:"files"`
			Next    string     `json:"next"`
		}
		if err := call(c, http.MethodGet, base+"/"+url.PathEscape(name)+"/files?"+q.Encode(), nil, &page); err != nil {
			return err
		}
		for _, f := range page.Folders {
			fmt.Printf("%s\n", f)
		}
		for _, f := range page.Files {
			fmt.Printf("%-48s %10s  %s\n", f.Key, human(f.Size), f.Modified.Local().Format("2 Jan 2006 15:04"))
		}
		if page.Next == "" {
			return nil
		}
		after = page.Next
	}
}

func storagePut(c config, base, file, target string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return errors.New("that's a folder; put files one at a time")
	}
	name, key := splitTarget(target)
	if key == "" || strings.HasSuffix(key, "/") {
		key += filepath.Base(file)
	}
	req, err := http.NewRequest(http.MethodPut, c.Server+base+"/"+url.PathEscape(name)+"/file?key="+url.QueryEscape(key), f)
	if err != nil {
		return err
	}
	req.ContentLength = fi.Size()
	typ := mime.TypeByExtension(path.Ext(key))
	if typ == "" {
		typ = "application/octet-stream"
	}
	req.Header.Set("Content-Type", typ)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("can't reach Pier at %s: %w", c.Server, err)
	}
	defer resp.Body.Close()
	var out struct {
		Error string `json:"error"`
		URL   string `json:"url"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode >= 300 {
		if out.Error != "" {
			return errors.New(out.Error)
		}
		return fmt.Errorf("Pier answered %s", resp.Status)
	}
	fmt.Printf("%s Uploaded %s to %s/%s, %s.\n", tick(), filepath.Base(file), name, key, human(fi.Size()))
	if out.URL != "" {
		fmt.Printf("  Anyone can read it at %s\n", out.URL)
	}
	return nil
}

func storageGet(c config, base, target, to string) error {
	name, key := splitTarget(target)
	if key == "" {
		return errors.New("say which file: pier storage get NAME/KEY")
	}
	var f fileInfo
	if err := call(c, http.MethodGet, base+"/"+url.PathEscape(name)+"/file?key="+url.QueryEscape(key), nil, &f); err != nil {
		return err
	}
	if to == "" {
		to = path.Base(key)
	}
	resp, err := http.Get(f.Download)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("R2 answered %s", resp.Status)
	}
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, resp.Body)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s Saved %s, %s.\n", tick(), to, human(n))
	return nil
}
