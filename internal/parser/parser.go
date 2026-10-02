// Package parser runs WASI parser artifacts on the requester side (spec §23).
//
// The module sees only the output bundle, mounted read-only at /in. It has no
// network, a fake clock, deterministic randomness, and bounded memory and
// time, so the same raw output always yields the same parsed bytes.
package parser

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"

	"github.com/chaeeundad/PFCN/pkg/contentid"
)

//go:embed artifacts/qe-parser.wasm
var qeParserWASM []byte

const (
	maxMemoryPages = 4096 // 256 MiB
	maxOutputBytes = 64 << 20
	timeout        = 2 * time.Minute
)

// Builtin returns an embedded parser artifact by its sha256 digest.
// Registry-based fetching of parser artifacts arrives with public records
// (Phase 4); until then only embedded parsers are runnable.
func Builtin(digest string) ([]byte, bool) {
	if contentid.SHA256(qeParserWASM) == digest {
		return qeParserWASM, true
	}
	return nil, false
}

// QEDigest is the digest of the embedded QE parser.
func QEDigest() string { return contentid.SHA256(qeParserWASM) }

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("parser output limit exceeded")
	}
	return b.Buffer.Write(p)
}

// Run executes wasm with outputDir mounted read-only at /in and returns stdout.
func Run(ctx context.Context, wasm []byte, outputDir string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(maxMemoryPages).
		WithCloseOnContextDone(true))
	defer rt.Close(context.Background())
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	stdout := &limitedBuffer{limit: maxOutputBytes}
	stderr := &limitedBuffer{limit: 1 << 20}
	cfg := wazero.NewModuleConfig().
		WithName("parser").
		WithArgs("parser").
		WithStdout(stdout).
		WithStderr(stderr).
		WithFSConfig(wazero.NewFSConfig().WithReadOnlyDirMount(outputDir, "/in"))
	mod, err := rt.InstantiateWithConfig(ctx, wasm, cfg)
	if mod != nil {
		mod.Close(context.Background())
	}
	if err != nil {
		var exit *sys.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 0 {
			return nil, fmt.Errorf("parser: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
		}
	}
	return stdout.Bytes(), nil
}
