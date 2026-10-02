package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Fake is an in-process runtime for tests. It copies Outputs into the output
// directory instead of running a container.
type Fake struct {
	PlatformName string
	Outputs      map[string][]byte
	ExitCode     int
	Images       map[string]bool
	Delay        time.Duration // simulated runtime; honours cancellation

	mu   sync.Mutex
	Runs []Spec
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Platform(context.Context) (string, error) { return f.PlatformName, nil }

func (f *Fake) HasImage(_ context.Context, image string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Images[image]
}

func (f *Fake) EnsureImage(_ context.Context, image string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Images == nil {
		f.Images = map[string]bool{}
	}
	f.Images[image] = true
	return nil
}

func (f *Fake) Run(ctx context.Context, s Spec) (Result, error) {
	f.mu.Lock()
	f.Runs = append(f.Runs, s)
	f.mu.Unlock()
	if f.Delay > 0 {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(f.Delay):
		}
	}
	if _, err := os.Stat(filepath.Join(s.InputDir, "pw.in")); err != nil {
		return Result{}, err
	}
	for name, data := range f.Outputs {
		if err := os.WriteFile(filepath.Join(s.OutputDir, name), data, 0o600); err != nil {
			return Result{}, err
		}
	}
	return Result{ExitCode: f.ExitCode, WallSeconds: 1}, nil
}
