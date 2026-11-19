// Package gitssh is pierd's git endpoint: an SSH server that only runs
// git-receive-pack and git-upload-pack against Pier's repositories, so
// `git push pier main` works with the SSH key a developer already has.
package gitssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"pier/internal/names"
)

type Server struct {
	Addr           string
	HostKeyPath    string                      // created on first start
	AuthorizedKeys string                      // one public key per line, re-read on every login
	Repo           func(project string) string // a project's bare repository
	Hook           string                      // the post-receive hook script, installed on every push
	Env            []string                    // extra environment for git (the hook reads it)
	Log            *slog.Logger
	ShellMessage   string // shown when someone opens a plain SSH session
}

// Serve accepts connections until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	signer, err := LoadOrCreateHostKey(s.HostKeyPath)
	if err != nil {
		return err
	}
	cfg := &ssh.ServerConfig{PublicKeyCallback: s.authorize}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.Log.Warn("ssh accept", "err", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.handle(c, cfg)
	}
}

// LoadOrCreateHostKey reads the server's host key, creating an ed25519 key
// the first time.
func LoadOrCreateHostKey(path string) (ssh.Signer, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		block, err := ssh.MarshalPrivateKey(priv, "pierd host key")
		if err != nil {
			return nil, err
		}
		raw = pem.EncodeToMemory(block)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(raw)
}

func (s *Server) authorize(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	raw, err := os.ReadFile(s.AuthorizedKeys)
	if err != nil {
		return nil, fmt.Errorf("no authorized keys: %w", err)
	}
	want := key.Marshal()
	for len(raw) > 0 {
		k, comment, _, rest, err := ssh.ParseAuthorizedKey(raw)
		if err != nil {
			break
		}
		raw = rest
		if string(k.Marshal()) == string(want) {
			return &ssh.Permissions{Extensions: map[string]string{"key": comment, "fp": ssh.FingerprintSHA256(key)}}, nil
		}
	}
	s.Log.Info("ssh key refused", "remote", meta.RemoteAddr().String(), "fp", ssh.FingerprintSHA256(key))
	return nil, errors.New("key not authorized")
}

func (s *Server) handle(c net.Conn, cfg *ssh.ServerConfig) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second)) // for the handshake only
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	c.SetDeadline(time.Time{})
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "pier only runs git")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.session(ch, creqs, conn.Permissions)
	}
}

func (s *Server) session(ch ssh.Channel, reqs <-chan *ssh.Request, perms *ssh.Permissions) {
	defer ch.Close()
	var protocol string // GIT_PROTOCOL, sent as an env request for git's protocol v2
	for req := range reqs {
		switch req.Type {
		case "env":
			var kv struct{ Name, Value string }
			if ssh.Unmarshal(req.Payload, &kv) == nil && kv.Name == "GIT_PROTOCOL" {
				protocol = kv.Value
			}
			req.Reply(true, nil)
		case "exec":
			var p struct{ Command string }
			if err := ssh.Unmarshal(req.Payload, &p); err != nil {
				req.Reply(false, nil)
				return
			}
			req.Reply(true, nil)
			code := s.exec(ch, p.Command, protocol, perms)
			ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
			return
		case "shell", "pty-req":
			if req.Type == "pty-req" {
				req.Reply(true, nil)
				continue
			}
			req.Reply(true, nil)
			io.WriteString(ch.Stderr(), s.ShellMessage)
			ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{1}))
			return
		default:
			req.Reply(false, nil)
		}
	}
}

// ParseCommand reads git's remote command, such as git-receive-pack '/blog.git'.
func ParseCommand(command string) (verb, project string, err error) {
	fields := strings.Fields(command)
	if len(fields) != 2 {
		return "", "", fmt.Errorf("pier only runs git push and git fetch")
	}
	verb = fields[0]
	if verb != "git-receive-pack" && verb != "git-upload-pack" {
		return "", "", fmt.Errorf("pier only runs git push and git fetch")
	}
	project = strings.Trim(fields[1], `'"`)
	project = strings.TrimSuffix(strings.TrimPrefix(project, "/"), ".git")
	if !names.ValidProject(project) {
		return "", "", fmt.Errorf("%q can't be a project name: use lowercase letters, numbers and single dashes, like status-page", project)
	}
	return verb, project, nil
}

func (s *Server) exec(ch ssh.Channel, command, protocol string, perms *ssh.Permissions) int {
	verb, project, err := ParseCommand(command)
	if err != nil {
		fmt.Fprintf(ch.Stderr(), "pier: %s\n", err)
		return 1
	}
	repo := s.Repo(project)
	if verb == "git-receive-pack" {
		if err := s.ensureRepo(repo); err != nil {
			s.Log.Error("create repo", "project", project, "err", err)
			fmt.Fprintf(ch.Stderr(), "pier: couldn't create the repository for %s\n", project)
			return 1
		}
	} else if _, err := os.Stat(repo); err != nil {
		fmt.Fprintf(ch.Stderr(), "pier: there's no project called %s yet; push to create it\n", project)
		return 1
	}

	cmd := exec.Command("git", strings.TrimPrefix(verb, "git-"), repo)
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.Env = append(cmd.Env, "PIER_PROJECT="+project)
	if protocol != "" {
		cmd.Env = append(cmd.Env, "GIT_PROTOCOL="+protocol)
	}
	cmd.Stdout, cmd.Stderr = ch, ch.Stderr()
	// Copy stdin ourselves: exec would otherwise wait for the client to close
	// its side, while the client waits for us to finish.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 1
	}
	if err := cmd.Start(); err != nil {
		s.Log.Error("start git", "err", err)
		return 1
	}
	go func() { io.Copy(stdin, ch); stdin.Close() }()
	s.Log.Info("git", "op", verb, "project", project, "key", perms.Extensions["key"])
	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}

// ensureRepo creates the bare repository if needed and (re)installs the
// post-receive hook, so the hook always points at the running pierd.
func (s *Server) ensureRepo(repo string) error {
	if _, err := os.Stat(repo); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
			return err
		}
		out, err := exec.Command("git", "init", "-q", "--bare", "--initial-branch=main", repo).CombinedOutput()
		if err != nil {
			return fmt.Errorf("git init: %v: %s", err, out)
		}
	}
	return os.WriteFile(filepath.Join(repo, "hooks", "post-receive"), []byte(s.Hook), 0o755)
}

// HookScript is the post-receive hook: it hands the pushed refs to pierd and
// prints what pierd says, which git shows the pusher as "remote:" lines.
func HookScript(pierd string) string {
	return "#!/bin/sh\nexec '" + strings.ReplaceAll(pierd, "'", `'\''`) + "' hook\n"
}
