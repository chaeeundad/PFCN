package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/chaeeundad/PFCN/internal/bundle"
	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/job"
	"github.com/chaeeundad/PFCN/internal/protocol"
	"github.com/chaeeundad/PFCN/internal/sandbox"
	"github.com/chaeeundad/PFCN/internal/seal"
	"github.com/chaeeundad/PFCN/internal/solver"
	"github.com/chaeeundad/PFCN/internal/solver/qe"
	"github.com/chaeeundad/PFCN/internal/store"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

const (
	maxStderrBytes  = 64 << 10
	maxProcsCeiling = 4096
)

func (a *Agent) startExecution(execID string) {
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.cancels[execID] = cancel
	a.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() {
			a.mu.Lock()
			delete(a.cancels, execID)
			a.mu.Unlock()
			cancel()
		}()
		if err := a.runExecution(ctx, execID); err != nil {
			a.log.Warn("execution failed", "exec", execID, "err", err)
			msg := err.Error()
			if terr := a.store.Transition(execID, []string{store.StateRunning}, store.StateFailed, store.Update{Error: &msg}, nil); terr != nil {
				a.log.Warn("could not record failure", "exec", execID, "err", terr)
			}
		}
		// Plaintext never outlives the run: keep only sealed/ (if any).
		dir := a.paths.ExecDir(execID)
		for _, p := range []string{"workspace", "input.bundle", "input.have", "output.bundle"} {
			os.RemoveAll(filepath.Join(dir, p))
		}
		if e, err := a.store.Get(execID); err == nil && e.State != store.StateSealed {
			os.RemoveAll(dir)
		}
	}()
}

// runExecution validates the input bundle, runs the solver, seals the output
// and signs the completion (§16.6, §28.2). Validation failures before the
// solver starts are recorded as FAILED without a completion.
func (a *Agent) runExecution(ctx context.Context, execID string) error {
	exec, err := a.store.Get(execID)
	if err != nil {
		return err
	}
	var lease protocol.Lease
	if err := exec.Lease.Decode(&lease); err != nil {
		return err
	}
	dir := a.paths.ExecDir(execID)
	ws := filepath.Join(dir, "workspace")
	sub := func(n string) string { return filepath.Join(ws, n) }
	for _, d := range []string{"bundle", "in", "pseudo", "scratch", "out"} {
		if err := os.MkdirAll(sub(d), 0o700); err != nil {
			return err
		}
	}
	// Containers may run under a different UID mapping; the workspace is
	// private to this execution and deleted at SEALED.
	for _, d := range []string{"scratch", "out"} {
		os.Chmod(sub(d), 0o777)
	}

	// 1. Re-validate the input; never trust requester-side validation.
	in, err := os.Open(filepath.Join(dir, "input.bundle"))
	if err != nil {
		return err
	}
	_, err = bundle.Extract(in, sub("bundle"), bundle.Limits{MaxFiles: 64, MaxTotalBytes: lease.InputBundleSize})
	in.Close()
	if err != nil {
		return fmt.Errorf("input bundle: %w", err)
	}
	cj, err := a.validateInput(&lease, sub("bundle"))
	if err != nil {
		return fmt.Errorf("input validation: %w", err)
	}
	structure, err := os.ReadFile(filepath.Join(sub("bundle"), "files", cj.System.Structure.Filename))
	if err != nil {
		return err
	}
	pwin, err := qe.BuildInput(cj, structure)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sub("in"), qe.InputFile), pwin, 0o644); err != nil {
		return err
	}
	for _, p := range cj.System.Pseudopotentials {
		data, err := os.ReadFile(filepath.Join(sub("bundle"), "files", p.Filename))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sub("pseudo"), p.Filename), data, 0o644); err != nil {
			return err
		}
	}
	for _, d := range []string{"in", "pseudo"} {
		os.Chmod(sub(d), 0o755)
	}

	// 2. Resolve the approved image by digest.
	manifest, err := a.manifestFor(lease.SolverManifestDigest)
	if err != nil {
		return err
	}
	if err := manifest.Manifest.Validate(); err != nil {
		return err
	}
	if rl := a.Revocations(); rl != nil {
		if err := rl.Check(manifest); err != nil {
			return err
		}
	}
	art, ok := manifest.Manifest.ArtifactFor(lease.Platform)
	if !ok || art.Digest != lease.ArtifactDigest {
		return errors.New("leased artifact is not in the solver manifest")
	}
	image := art.Reference + "@" + art.Digest
	pctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	err = a.rt.EnsureImage(pctx, image)
	cancel()
	if err != nil {
		return err
	}

	// 3. Run in the sandbox.
	cores := lease.Resources.CPUMillicores / 1000
	ep := manifest.Manifest.Entrypoints[lease.Entrypoint]
	started := time.Now()
	res, err := a.rt.Run(ctx, sandbox.Spec{
		Name: "pumat-" + execDirName(execID)[:16], Image: image, Entrypoint: lease.Entrypoint,
		InputDir: sub("in"), PseudoDir: sub("pseudo"), ScratchDir: sub("scratch"), OutputDir: sub("out"),
		CPUs: cores, MemoryBytes: lease.Resources.MemoryBytes,
		PIDs:     min(ep.MaxProcesses, maxProcsCeiling),
		Walltime: time.Duration(lease.MaxWalltimeSeconds) * time.Second,
		Env: map[string]string{
			"PUMAT_NPROCS": strconv.FormatInt(cores, 10),
			"PUMAT_POOLS":  strconv.FormatInt(qe.EstimatePools(cj), 10),
		},
	})
	if err != nil {
		return fmt.Errorf("sandbox: %w", err)
	}
	finished := time.Now()

	outcome := protocol.OutcomeSucceeded
	switch {
	case res.TimedOut:
		outcome = protocol.OutcomeWalltimeExceeded
	case res.OOMKilled:
		outcome = protocol.OutcomeResourceExceeded
	case res.ExitCode != 0:
		outcome = protocol.OutcomeSolverFailed
	}

	// 4. Build the output bundle.
	files, err := collectOutputs(sub("out"), lease.MaxOutputBytes)
	if errors.Is(err, errOutputTooLarge) {
		outcome = protocol.OutcomeResourceExceeded
		files, err = collectOutputs(sub("out"), -1) // stderr tail only
	}
	if err != nil {
		return err
	}
	envJSON, _ := json.MarshalIndent(map[string]any{
		"schema":          "pumat.execution-environment.v1",
		"exec_id":         execID,
		"artifact_digest": art.Digest,
		"platform":        lease.Platform,
		"cpu_model":       a.hw.CPUModel,
		"cpu_cores":       cores,
		"memory_bytes":    lease.Resources.MemoryBytes,
		"runtime":         a.rt.Name(),
		"exit_code":       res.ExitCode,
		"timed_out":       res.TimedOut,
		"oom_killed":      res.OOMKilled,
		"wall_seconds":    res.WallSeconds,
		"outcome":         outcome,
		"started_at":      protocol.Now(started),
		"finished_at":     protocol.Now(finished),
	}, "", "  ")
	files = append(files, bundle.File{Name: "execution-environment.json", Data: append(envJSON, '\n')})
	tarData, err := bundle.Build(files)
	if err != nil {
		return err
	}
	plainPath := filepath.Join(dir, "output.bundle")
	if err := os.WriteFile(plainPath, tarData, 0o600); err != nil {
		return err
	}
	outDigest := bundle.Digest(tarData, bundle.ChunkSize)

	// 5. Seal to the requester's per-execution key.
	sd := filepath.Join(dir, "sealed")
	if err := os.MkdirAll(sd, 0o700); err != nil {
		return err
	}
	sealer, err := seal.NewSealer(lease.ResultRecipientKey, execID)
	if err != nil {
		return err
	}
	sealedPath := filepath.Join(sd, "bundle.sealed")
	if err := sealer.SealFile(plainPath, sealedPath, bundle.ChunkSize); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sd, "enc"), sealer.Enc, 0o600); err != nil {
		return err
	}
	_, _, sealedDigest, err := bundle.FileDigest(sealedPath, seal.SealedChunkSize(bundle.ChunkSize))
	if err != nil {
		return err
	}

	// 6. Sign the completion and persist SEALED before deleting plaintext.
	comp := protocol.Completion{
		Schema: protocol.SchemaCompletion, ExecID: execID, CalcID: lease.CalcID,
		LeaseID: protocol.LeaseID(exec.Lease), RequesterPeerID: lease.RequesterPeerID,
		WorkerPeerID: lease.WorkerPeerID, SolverManifestDigest: lease.SolverManifestDigest,
		ArtifactDigest: art.Digest, Platform: lease.Platform,
		InputBundleDigest: lease.InputBundleDigest, OutputBundleDigest: outDigest,
		SealedBundleDigest: sealedDigest, Outcome: outcome, ExitCode: int64(res.ExitCode),
		ResourceClaim: protocol.ResourceClaim{CPUMillicores: lease.Resources.CPUMillicores, WallSeconds: res.WallSeconds},
		StartedAt:     protocol.Now(started), FinishedAt: protocol.Now(finished),
	}
	compEnv, err := envelope.Sign(a.id, protocol.SchemaCompletion, comp)
	if err != nil {
		return err
	}
	now := time.Now()
	retention := now.Add(time.Duration(lease.ResultRetentionSeconds) * time.Second)
	if err := a.store.Transition(execID, []string{store.StateRunning}, store.StateSealed, store.Update{
		Completion: compEnv, SealedAt: &now, RetentionDeadline: &retention,
	}, map[string]any{"outcome": outcome, "receipt_id": protocol.ReceiptID(compEnv)}); err != nil {
		return err
	}
	a.log.Info("result sealed", "exec", execID, "outcome", outcome, "wall_seconds", res.WallSeconds)
	return nil
}

// validateInput checks the canonical job against the lease and file digests.
func (a *Agent) validateInput(lease *protocol.Lease, bundleDir string) (*job.Canonical, error) {
	raw, err := os.ReadFile(filepath.Join(bundleDir, "input", "canonical-job.json"))
	if err != nil {
		return nil, errors.New("input/canonical-job.json missing")
	}
	cj, err := job.ParseCanonical(raw)
	if err != nil {
		return nil, err
	}
	if err := cj.Validate(a.reg); err != nil {
		return nil, err
	}
	calc, err := cj.CalcID()
	if err != nil {
		return nil, err
	}
	switch {
	case calc != lease.CalcID:
		return nil, errors.New("calc_id does not match the lease")
	case cj.Solver.ManifestDigest != lease.SolverManifestDigest || cj.Solver.Entrypoint != lease.Entrypoint:
		return nil, errors.New("solver does not match the lease")
	case cj.Metadata.Visibility != lease.Visibility:
		return nil, errors.New("visibility does not match the lease")
	case cj.Resources.CPUCores*1000 != lease.Resources.CPUMillicores || cj.Resources.MemoryBytes != lease.Resources.MemoryBytes ||
		cj.Resources.WalltimeSeconds != lease.MaxWalltimeSeconds:
		return nil, errors.New("resources do not match the lease")
	}
	refs := []job.FileRef{cj.System.Structure.FileRef}
	for _, p := range cj.System.Pseudopotentials {
		refs = append(refs, p.FileRef)
	}
	for _, r := range refs {
		data, err := os.ReadFile(filepath.Join(bundleDir, "files", r.Filename))
		if err != nil {
			return nil, fmt.Errorf("file %s missing from bundle", r.Filename)
		}
		if int64(len(data)) != r.Size || contentid.SHA256(data) != r.Digest {
			return nil, fmt.Errorf("file %s does not match its pinned digest", r.Filename)
		}
	}
	return cj, nil
}

func (a *Agent) manifestFor(digest string) (*solver.Verified, error) {
	cat, err := a.solverCatalog()
	if err != nil {
		return nil, err
	}
	if v, ok := cat[digest]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("solver manifest %s is not installed", digest)
}

var errOutputTooLarge = errors.New("output exceeds the leased limit")

// collectOutputs reads /work/out. limit < 0 returns only the stderr tail.
func collectOutputs(dir string, limit int64) ([]bundle.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var files []bundle.File
	var total int64
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue // symlinks and directories from the solver are ignored
		}
		if limit < 0 && e.Name() != "stderr.txt" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if e.Name() == "stderr.txt" && len(data) > maxStderrBytes {
			data = append([]byte("[truncated]\n"), data[len(data)-maxStderrBytes:]...)
		}
		total += int64(len(data))
		if limit >= 0 && total > limit {
			return nil, errOutputTooLarge
		}
		files = append(files, bundle.File{Name: "output/" + e.Name(), Data: bytes.Clone(data)})
	}
	return files, nil
}
