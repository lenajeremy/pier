package build

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Runner runs a shell command inside a checkout, writing its output to log.
// env holds NAME=value pairs for the build, the project's variables.
type Runner interface {
	Run(ctx context.Context, dir, command string, env []string, log io.Writer) error
	// Name describes where builds run, for the log.
	Name() string
}

// Docker runs each command in a fresh container that has only the checkout
// and a shared package cache mounted. The container is removed afterwards.
type Docker struct {
	Image       string // e.g. node:22-alpine
	CacheVolume string // a named volume for npm, pnpm and yarn caches
}

func (d Docker) Name() string { return "a throwaway " + d.Image + " container" }

func (d Docker) Run(ctx context.Context, dir, command string, env []string, log io.Writer) error {
	cache := "/cache"
	// Docker creates empty volumes as root. Give the service account a writable
	// subdirectory, including on the first build following a cache cleanup.
	if runtime.GOOS == "linux" && os.Getuid() != 0 {
		cache = fmt.Sprintf("/cache/user-%d", os.Getuid())
		owner := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
		prepare := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--user", "0:0", "-v", d.CacheVolume+":/cache", "--entrypoint", "sh", d.Image, "-c", `mkdir -p "$1" && chown "$2" "$1"`, "sh", cache, owner)
		prepare.Stdout, prepare.Stderr = log, log
		if err := prepare.Run(); err != nil {
			return fmt.Errorf("prepare package cache: %w", err)
		}
	}
	cname := "pier-build-" + strings.ToLower(rand.Text())
	args := []string{"run", "--rm", "--name", cname, "--network", "bridge",
		"--security-opt", "no-new-privileges", "--pids-limit", "512",
		"-v", dir + ":/app", "-w", "/app",
		"-v", d.CacheVolume + ":/cache",
		"-e", "CI=1",
		"-e", "NO_COLOR=1",
		"-e", "FORCE_COLOR=0",
		"-e", "npm_config_cache=" + cache + "/npm",
		"-e", "npm_config_update_notifier=false",
		"-e", "YARN_CACHE_FOLDER=" + cache + "/yarn",
		"-e", "COREPACK_HOME=" + cache + "/corepack",
		"-e", "COREPACK_ENABLE_DOWNLOAD_PROMPT=0",
		"-e", "PNPM_HOME=" + cache + "/pnpm",
	}
	if runtime.GOOS == "linux" {
		// Keep the build's files owned by pierd's user, so it can move them.
		args = append(args, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "-e", "HOME=/tmp")
	}
	// Pass single-line variables through a 0600 env-file so user variable names
	// never pollute the host docker CLI's own environment. Any multiline value
	// falls back to -e NAME only when its name cannot reconfigure docker.
	var singleLine, multiLine []string
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if strings.ContainsAny(value, "\r\n") {
			if !hostSensitiveEnv(name) {
				multiLine = append(multiLine, kv)
				args = append(args, "-e", name)
			}
			continue
		}
		singleLine = append(singleLine, kv)
	}
	if len(singleLine) > 0 {
		ef, err := os.CreateTemp("", "pier-build-env-*")
		if err != nil {
			return fmt.Errorf("create build env file: %w", err)
		}
		envPath := ef.Name()
		defer os.Remove(envPath)
		if err := ef.Chmod(0o600); err != nil {
			ef.Close()
			return fmt.Errorf("secure build env file: %w", err)
		}
		_, werr := io.WriteString(ef, strings.Join(singleLine, "\n")+"\n")
		cerr := ef.Close()
		if werr != nil {
			return fmt.Errorf("write build env file: %w", werr)
		}
		if cerr != nil {
			return fmt.Errorf("close build env file: %w", cerr)
		}
		args = append(args, "--env-file", envPath)
	}
	args = append(args, d.Image, "sh", "-c", command)
	cmd := exec.CommandContext(ctx, "docker", args...)
	if len(multiLine) > 0 {
		cmd.Env = append(os.Environ(), multiLine...)
	}
	cmd.Stdout, cmd.Stderr = log, log
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		// Killing the docker CLI does not stop the container inside the daemon.
		cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = exec.CommandContext(cleanCtx, "docker", "rm", "-f", cname).Run()
		cancel()
	}
	return err
}

func hostSensitiveEnv(name string) bool {
	for _, prefix := range []string{"DOCKER_", "LD_", "DYLD_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	switch strings.ToUpper(name) {
	case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "GODEBUG", "PATH", "HOME":
		return true
	}
	return false
}

// Local runs commands directly on the host. It's for development on a
// machine without Docker; it gives the build no isolation at all.
type Local struct{}

func (Local) Name() string { return "the host (no isolation)" }

func (Local) Run(ctx context.Context, dir, command string, env []string, log io.Writer) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "CI=1", "NO_COLOR=1", "FORCE_COLOR=0", "COREPACK_ENABLE_DOWNLOAD_PROMPT=0"), env...)
	cmd.Stdout, cmd.Stderr = log, log
	return cmd.Run()
}
