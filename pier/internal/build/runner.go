package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
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
	args := []string{"run", "--rm", "--network", "bridge",
		"-v", dir + ":/app", "-w", "/app",
		"-v", d.CacheVolume + ":/cache",
		"-e", "CI=1",
		"-e", "NO_COLOR=1",
		"-e", "FORCE_COLOR=0",
		"-e", "npm_config_cache=/cache/npm",
		"-e", "npm_config_update_notifier=false",
		"-e", "YARN_CACHE_FOLDER=/cache/yarn",
		"-e", "COREPACK_HOME=/cache/corepack",
		"-e", "COREPACK_ENABLE_DOWNLOAD_PROMPT=0",
		"-e", "PNPM_HOME=/cache/pnpm",
	}
	if runtime.GOOS == "linux" {
		// Keep the build's files owned by pierd's user, so it can move them.
		args = append(args, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "-e", "HOME=/tmp")
	}
	// Variables are passed as -e NAME, with the value in docker's own
	// environment, so a value never appears on a command line.
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		args = append(args, "-e", name)
	}
	args = append(args, d.Image, "sh", "-c", command)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = log, log
	return cmd.Run()
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
