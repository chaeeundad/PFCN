// Package sandbox runs approved solver images under strict isolation
// (spec §17.2).
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Spec describes one sandboxed solver run.
type Spec struct {
	Name        string // container name
	Image       string // reference@sha256:digest
	Entrypoint  string // entrypoint name declared in the solver manifest
	InputDir    string // mounted read-only at /work/in
	PseudoDir   string // mounted read-only at /work/pseudo
	ScratchDir  string // mounted read-write at /work/scratch
	OutputDir   string // mounted read-write at /work/out
	CPUs        int64
	MemoryBytes int64
	PIDs        int64
	Walltime    time.Duration
	Env         map[string]string // only PUMAT_* launcher variables
	// DiskLimitBytes bounds scratch+output usage; 0 disables the watchdog.
	// Bind mounts cannot carry a size limit portably, so usage is polled.
	DiskLimitBytes int64
}

// Result is the outcome of a run.
type Result struct {
	ExitCode     int
	TimedOut     bool
	OOMKilled    bool
	DiskExceeded bool
	WallSeconds  int64
}

// Runtime executes sandboxed runs.
type Runtime interface {
	Name() string
	// Platform returns the execution platform, e.g. "linux/arm64".
	Platform(ctx context.Context) (string, error)
	// HasImage reports whether ref@digest is present locally.
	HasImage(ctx context.Context, image string) bool
	// EnsureImage pulls ref@digest if needed; the engine verifies content digests.
	EnsureImage(ctx context.Context, image string) error
	Run(ctx context.Context, s Spec) (Result, error)
}

// Container runs solvers with podman or docker.
type Container struct {
	engine string
	path   string
}

// Detect picks an engine: podman is preferred because it runs rootless by
// default.
func Detect(engine string) (*Container, error) {
	cands := []string{"podman", "docker"}
	if engine != "" && engine != "auto" {
		cands = []string{engine}
	}
	for _, c := range cands {
		if p, err := exec.LookPath(c); err == nil {
			return &Container{engine: c, path: p}, nil
		}
	}
	return nil, fmt.Errorf("sandbox: no container engine found (tried %s)", strings.Join(cands, ", "))
}

func (c *Container) Name() string { return c.engine }

func (c *Container) cmd(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, c.path, args...)
}

func (c *Container) output(ctx context.Context, args ...string) (string, error) {
	var stderr bytes.Buffer
	cmd := c.cmd(ctx, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("sandbox: %s %s: %w: %s", c.engine, args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Container) Platform(ctx context.Context) (string, error) {
	format := "{{.OSType}}/{{.Architecture}}"
	if c.engine == "podman" {
		format = "{{.Host.OS}}/{{.Host.Arch}}"
	}
	out, err := c.output(ctx, "info", "--format", format)
	if err != nil {
		return "", err
	}
	return NormalizePlatform(out), nil
}

// NormalizePlatform maps engine architecture names to OCI names.
func NormalizePlatform(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	r := strings.NewReplacer("x86_64", "amd64", "aarch64", "arm64")
	return r.Replace(s)
}

func (c *Container) HasImage(ctx context.Context, image string) bool {
	return c.cmd(ctx, "image", "inspect", image).Run() == nil
}

func (c *Container) EnsureImage(ctx context.Context, image string) error {
	if !strings.Contains(image, "@sha256:") {
		return errors.New("sandbox: images must be referenced by digest")
	}
	if c.HasImage(ctx, image) {
		return nil
	}
	if _, err := c.output(ctx, "pull", image); err != nil {
		return err
	}
	if !c.HasImage(ctx, image) {
		return fmt.Errorf("sandbox: %s not present after pull", image)
	}
	return nil
}

// Args builds the hardened run arguments (exported for tests and doctor).
func (c *Container) Args(s Spec, uid, gid int) []string {
	args := []string{"run", "--detach", "--name", s.Name,
		"--network", "none",
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--ipc", "private",
		"--pids-limit", strconv.FormatInt(s.PIDs, 10),
		"--memory", strconv.FormatInt(s.MemoryBytes, 10),
		"--memory-swap", strconv.FormatInt(s.MemoryBytes, 10),
		"--cpus", strconv.FormatInt(s.CPUs, 10),
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		"--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=268435456",
		"--pull", "never",
		"--volume", s.InputDir + ":/work/in:ro",
		"--volume", s.PseudoDir + ":/work/pseudo:ro",
		"--volume", s.ScratchDir + ":/work/scratch:rw",
		"--volume", s.OutputDir + ":/work/out:rw",
	}
	if c.engine == "podman" {
		args = append(args, "--userns", "keep-id")
	}
	for k, v := range s.Env {
		args = append(args, "--env", k+"="+v)
	}
	return append(args, s.Image, s.Entrypoint)
}

func (c *Container) Run(ctx context.Context, s Spec) (Result, error) {
	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 {
		return Result{}, errors.New("sandbox: refusing to run solver jobs as root; run the agent as the dedicated pumat user")
	}
	for k := range s.Env {
		if !strings.HasPrefix(k, "PUMAT_") {
			return Result{}, fmt.Errorf("sandbox: environment variable %q is not allowed", k)
		}
	}
	start := time.Now()
	if _, err := c.output(ctx, c.Args(s, uid, gid)...); err != nil {
		return Result{}, err
	}
	defer c.cmd(context.Background(), "rm", "--force", s.Name).Run()

	waitCtx, cancel := context.WithTimeout(ctx, s.Walltime)
	defer cancel()
	diskHit := make(chan struct{})
	if s.DiskLimitBytes > 0 {
		go watchDisk(waitCtx, []string{s.ScratchDir, s.OutputDir}, s.DiskLimitBytes, func() {
			close(diskHit)
			c.cmd(context.Background(), "kill", s.Name).Run()
		})
	}
	out, werr := c.output(waitCtx, "wait", s.Name)
	res := Result{}
	select {
	case <-diskHit:
		res.DiskExceeded = true
	default:
	}
	if werr != nil {
		c.cmd(context.Background(), "kill", s.Name).Run()
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			res.TimedOut = true
		} else if ctx.Err() != nil {
			return res, ctx.Err()
		} else {
			return res, werr
		}
		out, _ = c.output(context.Background(), "wait", s.Name)
	}
	res.WallSeconds = int64(time.Since(start).Round(time.Second) / time.Second)
	if code, err := strconv.Atoi(strings.TrimSpace(firstLine(out))); err == nil {
		res.ExitCode = code
	} else if !res.TimedOut {
		return res, fmt.Errorf("sandbox: unexpected wait output %q", out)
	}
	if oom, err := c.output(context.Background(), "inspect", "--format", "{{.State.OOMKilled}}", s.Name); err == nil {
		res.OOMKilled = oom == "true"
	}
	return res, nil
}

// watchDisk polls the total size of dirs and calls exceeded once over limit.
func watchDisk(ctx context.Context, dirs []string, limit int64, exceeded func()) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if DirSize(dirs...) > limit {
			exceeded()
			return
		}
	}
}

// DirSize returns the total size of regular files under dirs.
func DirSize(dirs ...string) int64 {
	var total int64
	for _, d := range dirs {
		filepath.WalkDir(d, func(_ string, e os.DirEntry, err error) error {
			if err == nil && e.Type().IsRegular() {
				if fi, err := e.Info(); err == nil {
					total += fi.Size()
				}
			}
			return nil
		})
	}
	return total
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
