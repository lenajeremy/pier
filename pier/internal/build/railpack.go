package build

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Use the same release for the planner and the BuildKit frontend.
const railpackImage = "ghcr.io/railwayapp/railpack-frontend:v0.43.0"

type Railpack struct {
	Image string // Existing Pier image for projects with explicit commands or tooling.
}

type railLayer struct {
	Local   bool     `json:"local,omitempty"`
	Image   string   `json:"image,omitempty"`
	Step    string   `json:"step,omitempty"`
	Include []string `json:"include,omitempty"`
}

// Keep provider-specific step fields when reading the upstream plan.
type railPlan struct {
	Steps   []map[string]any `json:"steps,omitempty"`
	Caches  json.RawMessage  `json:"caches,omitempty"`
	Exclude []string         `json:"exclude,omitempty"`
	Secrets []string         `json:"secrets,omitempty"`
	Deploy  map[string]any   `json:"deploy"`
}

// Export builds in a fresh stage outside the checkout. Only the returned
// directory is published. No image or running site container is retained.
func (r Railpack) Export(ctx context.Context, dir, stage, project string, p Plan, overrides Overrides, env []string, log io.Writer) (string, error) {
	var plan railPlan
	input := railLayer{Step: "build", Include: []string{p.Output}}
	output := p.Output
	switch {
	case p.Install == "" && p.Build == "":
		input = railLayer{Local: true, Include: []string{p.Output}}
		output = filepath.Base(output) // Local inputs go under /app by basename.
	case overrides.Install != "" || overrides.Build != "" || p.Tooling != "" || r.Image != DefaultNodeImage || overrides.Preset != "":
		plan.Steps = []map[string]any{r.commandStep(p)}
	default:
		if err := prepare(ctx, dir, p.Build, &plan, false, log); err != nil {
			return "", err
		}
	}
	// An empty base creates a scratch filesystem. The local exporter writes
	// just this layer, skipping the runtime image and Caddy setup entirely.
	plan.Deploy = map[string]any{"base": railLayer{}, "inputs": []railLayer{input}}
	if input.Local {
		env = nil
	}
	if err := solve(ctx, dir, stage, project, &plan, env, []string{"--output", "type=local,dest=files"}, log); err != nil {
		return "", err
	}
	files := filepath.Join(stage, "files")
	root, err := os.OpenRoot(files)
	if err != nil {
		return "", err
	}
	defer root.Close()
	relative := filepath.Join("app", output)
	dir = filepath.Join(files, relative)
	info, err := root.Lstat(relative)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("the build did not produce a directory at %s/; check the output folder in the project's settings", p.Output)
	}
	if err := CheckTree(dir); err != nil {
		return "", fmt.Errorf("check exported folder %q: %w", p.Output, err)
	}
	return dir, nil
}

// BuildImage preserves the full Railpack deployment layer and start command,
// or delegates a repository Dockerfile to Docker's standard frontend.
func BuildImage(ctx context.Context, dir, stage, project, tag string, p Plan, env []string, log io.Writer) error {
	options := []string{"--load", "--tag", tag}
	var plan *railPlan
	if p.Runtime == "server" {
		plan = &railPlan{}
		if err := prepare(ctx, dir, p.Build, plan, true, log); err != nil {
			return err
		}
		if p.Start != "" {
			if plan.Deploy == nil {
				plan.Deploy = map[string]any{}
			}
			plan.Deploy["startCommand"] = p.Start
		}
		// Railpack v0.43 emits an LLB MergeOp for disjoint image layers.
		// Docker's classic image store only supports its sequential copy path.
		// A local input with no selected paths copies no files and selects
		// that path without changing the image or requiring another daemon.
		useCopies := func(stage map[string]any, minimum int) {
			if inputs, ok := stage["inputs"].([]any); ok && len(inputs) >= minimum {
				stage["inputs"] = append(inputs, railLayer{Local: true})
			}
		}
		useCopies(plan.Deploy, 1) // The deploy base is stored separately.
		for _, step := range plan.Steps {
			useCopies(step, 2)
		}
	} else if len(env) > 0 {
		// Dockerfile secret mounts do not automatically invalidate cached RUN steps.
		options = append(options, "--no-cache")
	}
	return solve(ctx, dir, stage, project, plan, env, options, log)
}

func solve(ctx context.Context, dir, stage, project string, plan *railPlan, env, options []string, log io.Writer) error {
	args := []string{"buildx", "build", "--progress=plain"}
	if plan != nil {
		args = append(args, "--build-arg", "BUILDKIT_SYNTAX="+railpackImage, "--build-arg", "cache-key=pier-"+project, "--file", "-")
		plan.Secrets = nil
	}
	args = append(args, options...)
	clientEnv := os.Environ()
	if len(env) > 0 {
		// A fresh nonce invalidates secret-dependent layers even on redeploy.
		// Use private client variable names so project variables cannot reconfigure Docker.
		args = append(args, "--build-arg", "secrets-hash="+filepath.Base(stage))
		for i, kv := range env {
			name, value, _ := strings.Cut(kv, "=")
			key := fmt.Sprintf("PIER_RAILPACK_SECRET_%d", i)
			clientEnv = append(clientEnv, key+"="+value)
			args = append(args, "--secret", "id="+name+",env="+key)
			if plan != nil {
				plan.Secrets = append(plan.Secrets, name)
			}
		}
	}
	cmd := exec.CommandContext(ctx, "docker", append(args, dir)...)
	cmd.Dir, cmd.Env = stage, clientEnv
	if plan != nil {
		raw, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		cmd.Stdin = bytes.NewReader(raw)
	}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building image or files: %w", err)
	}
	return nil
}

// commandStep preserves the owner's image, commands, and package-manager version,
// including projects with a build command and no package.json.
func (r Railpack) commandStep(p Plan) map[string]any {
	commands := []map[string]string{}
	for _, command := range []string{p.Install, p.Build} {
		if command != "" {
			quoted := strings.ReplaceAll(command, "'", "'\\''")
			commands = append(commands, map[string]string{"cmd": "sh -c '" + quoted + "'", "customName": command})
		}
	}
	return map[string]any{
		"name": "build", "commands": commands, "secrets": []string{"*"},
		"variables": map[string]string{"CI": "1", "NO_COLOR": "1", "FORCE_COLOR": "0", "COREPACK_ENABLE_DOWNLOAD_PROMPT": "0"},
		"inputs":    []railLayer{{Image: r.Image}, {Local: true, Include: []string{"."}}},
	}
}

func prepare(ctx context.Context, dir, command string, plan *railPlan, server bool, log io.Writer) error {
	args := []string{"run", "--rm", "--init", "--security-opt", "no-new-privileges", "--entrypoint", "/railpack",
		"-v", dir + ":/app:ro", railpackImage, "prepare", "/app",
		"--hide-pretty-plan", "--plan-out", "/dev/stdout", "--info-out", "/tmp/info.json"}
	if command != "" {
		args = append(args, "--build-cmd", command)
	}
	if server {
		args = append(args, "--error-missing-start")
	} else {
		args = append(args, "--env", "RAILPACK_NODE_VERSION=22")
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Stderr = log
	raw, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("Railpack prepare: %w", err)
	}
	if err := json.Unmarshal(raw, plan); err != nil {
		return fmt.Errorf("read Railpack plan: %w", err)
	}
	// An install script can consume a project variable as well as a build script.
	for _, step := range plan.Steps {
		if step["name"] == "install" || step["name"] == "build" {
			step["secrets"] = []string{"*"}
		}
	}
	return nil
}

// CheckTree ensures a checkout or published directory is self-contained.
// Relative links must still resolve inside that directory after publication.
func CheckTree(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if _, err := root.Stat(path); err != nil {
				return fmt.Errorf("invalid link %s: %w", path, err)
			}
		} else if !entry.IsDir() && !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported file %s", path)
		}
		return nil
	})
}
