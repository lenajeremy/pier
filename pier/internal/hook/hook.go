// Package hook is the client side of git's post-receive hook. git runs it
// after a push lands; it tells pierd which branches moved and copies pierd's
// narration back, which git shows the pusher as "remote:" lines.
package hook

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

// Push is one branch that moved.
type Push struct {
	Project string `json:"project"`
	Branch  string `json:"branch"`
	Commit  string `json:"commit"`
}

const zero = "0000000000000000000000000000000000000000"

// Run reads git's "<old> <new> <ref>" lines from stdin and asks pierd, over
// its local socket, to deploy each updated branch. It returns the exit code.
func Run(stdin io.Reader, stdout io.Writer) int {
	socket, project := os.Getenv("PIER_SOCKET"), os.Getenv("PIER_PROJECT")
	if socket == "" || project == "" {
		fmt.Fprintln(stdout, "pier    this hook only runs inside pierd")
		return 1
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}

	code := 0
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 || !strings.HasPrefix(f[2], "refs/heads/") {
			continue
		}
		branch, commit := strings.TrimPrefix(f[2], "refs/heads/"), f[1]
		if commit == zero || strings.Trim(commit, "0") == "" {
			fmt.Fprintf(stdout, "pier    %s was deleted; its address keeps serving until you remove it\n", branch)
			continue
		}
		body, _ := json.Marshal(Push{Project: project, Branch: branch, Commit: commit})
		resp, err := client.Post("http://pierd/internal/push", "application/json", bytes.NewReader(body))
		if err != nil {
			fmt.Fprintf(stdout, "pier    couldn't reach pierd: %v\n", err)
			code = 1
			continue
		}
		io.Copy(stdout, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Trailer.Get("Pier-Result") != "ok" {
			code = 1
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(stdout, "pier    couldn't read the pushed refs: %v\n", err)
		return 1
	}
	return code
}
