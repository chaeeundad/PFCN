package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chaeeundad/PFCN/internal/config"
	"github.com/chaeeundad/PFCN/internal/identity"
	"github.com/chaeeundad/PFCN/internal/parser"
	"github.com/chaeeundad/PFCN/internal/protocol"
	"github.com/chaeeundad/PFCN/internal/qeparse"
	"github.com/chaeeundad/PFCN/internal/sandbox"
	"github.com/chaeeundad/PFCN/internal/solver"
	"github.com/chaeeundad/PFCN/internal/store"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

const testArtifact = "sha256:4444444444444444444444444444444444444444444444444444444444444444"

type testNet struct {
	requester, worker *Agent
	fake              *sandbox.Fake
	jobPath           string
	manifestDigest    string
}

func newNode(t *testing.T, signer string, rt sandbox.Runtime, tweaks ...func(*config.Config)) *Agent {
	t.Helper()
	home := t.TempDir()
	res, err := Init(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg := res.Config
	cfg.Listen = []string{"/ip4/127.0.0.1/tcp/0"}
	cfg.Trust.SolverSigners = []string{signer}
	cfg.Resources.CPU = 1
	cfg.Resources.Memory = "4GiB"
	cfg.Requester.ResultsDir = filepath.Join(home, "results")
	for _, tw := range tweaks {
		tw(cfg)
	}
	if err := cfg.Save(Paths{Home: home}.Config()); err != nil {
		t.Fatal(err)
	}
	a, err := Open(Options{Home: home, Runtime: rt, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), FetchInterval: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func setup(t *testing.T) *testNet {
	t.Helper()
	signKey, err := identity.Generate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := solver.FromYAML([]byte(`schema: pumat.solver.v1
name: quantum-espresso
version: "7.4.1"
source: {repository: https://gitlab.com/QEF/q-e, commit: qe-7.4.1, license: GPL-2.0-or-later}
artifacts:
  - {platform: linux/arm64, reference: example.invalid/qe, mediaType: application/vnd.oci.image.manifest.v1+json, digest: `+testArtifact+`}
entrypoints:
  pw.x: {launcher: mpi, network: deny, maxProcesses: 64, maxWalltime: 24h}
security: {rootFilesystem: readOnly, network: deny, privileged: false, hostMounts: false}
numerics: {blas: openblas, blasVersion: "0.3.21", cpuDispatch: generic}
parser: {name: pumat-qe-parser, version: "0.1.0", format: wasm, reference: builtin, digest: "`+parser.QEDigest()+`"}
attestations: {sbom: "", buildProvenance: ""}
`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	env, err := solver.Sign(m, signKey)
	if err != nil {
		t.Fatal(err)
	}

	xml, _ := os.ReadFile("../qeparse/testdata/data-file-schema.xml")
	out, _ := os.ReadFile("../qeparse/testdata/stdout.txt")
	fake := &sandbox.Fake{PlatformName: "linux/arm64", Outputs: map[string][]byte{
		"stdout.txt": out, "stderr.txt": nil, "data-file-schema.xml": xml,
	}}
	n := &testNet{
		requester: newNode(t, signKey.PeerID.String(), nil),
		worker:    newNode(t, signKey.PeerID.String(), fake),
		fake:      fake,
	}
	for _, a := range []*Agent{n.requester, n.worker} {
		v, err := a.AddSolver(env)
		if err != nil {
			t.Fatal(err)
		}
		n.manifestDigest = v.Digest
	}

	dir := t.TempDir()
	cif, _ := os.ReadFile("../../examples/qe-si-scf/silicon.cif")
	os.WriteFile(filepath.Join(dir, "silicon.cif"), cif, 0o644)
	upf := []byte("<UPF test pseudo>")
	os.WriteFile(filepath.Join(dir, "Si.upf"), upf, 0o644)
	n.jobPath = filepath.Join(dir, "job.yaml")
	os.WriteFile(n.jobPath, []byte(`apiVersion: pumat.org/v1alpha1
kind: QuantumEspressoJob
metadata: {name: si-scf, visibility: public}
solver: {name: quantum-espresso, version: "7.4.1", manifestDigest: "`+n.manifestDigest+`", entrypoint: pw.x}
resources: {cpu: {cores: 1}, memory: 1GiB, walltime: 10m}
system:
  structure: {file: silicon.cif}
  pseudopotentials:
    Si: {filename: Si.upf, digest: "`+contentid.SHA256(upf)+`"}
calculation:
  type: scf
  parameters: {ecutwfc: 30, ecutrho: 240, kpoints: [4, 4, 4]}
publication: {input: true, rawOutput: true, parsedOutput: true, provenance: true, license: CC-BY-4.0}
`), 0o644)
	return n
}

func (n *testNet) start(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		n.requester.Close()
		n.worker.Close()
	})
	for _, a := range []*Agent{n.requester, n.worker} {
		if err := a.Start(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return ctx
}

func waitState(t *testing.T, a *Agent, execID, want string, timeout time.Duration) *store.Execution {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e, err := a.store.Get(execID)
		if err == nil && e.State == want {
			return e
		}
		if err == nil && store.Terminal(e.State) && e.State != want {
			t.Fatalf("execution reached %s (%s), want %s", e.State, e.Error, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
	e, _ := a.store.Get(execID)
	t.Fatalf("timeout waiting for %s; state %+v", want, e)
	return nil
}

func TestTwoPeerExecution(t *testing.T) {
	n := setup(t)
	ctx := n.start(t)

	// A paused worker refuses leases.
	if _, err := n.requester.Submit(ctx, SubmitRequest{JobPath: n.jobPath, Peer: n.worker.Addrs()}, nil); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("expected paused rejection, got %v", err)
	}
	if err := n.worker.SetMode(ModeAvailable); err != nil {
		t.Fatal(err)
	}

	var steps []string
	res, err := n.requester.Submit(ctx, SubmitRequest{JobPath: n.jobPath, Peer: n.worker.Addrs()}, func(s string) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	exec := waitState(t, n.requester, res.ExecID, store.StateCompleted, 120*time.Second)
	wexec := waitState(t, n.worker, res.ExecID, store.StateCompleted, 30*time.Second)

	// Sandbox received exactly the leased limits and fixed launcher env.
	run := n.fake.Runs[0]
	if run.Image != "example.invalid/qe@"+testArtifact || run.CPUs != 1 || run.MemoryBytes != 1<<30 || run.Env["PUMAT_NPROCS"] != "1" {
		t.Fatalf("unexpected sandbox spec %+v", run)
	}

	// Worker kept no job data (§28.2).
	if _, err := os.Stat(n.worker.paths.ExecDir(res.ExecID)); !os.IsNotExist(err) {
		t.Fatal("worker execution directory must be deleted after completion")
	}
	// Requester recipient key destroyed after decryption.
	if _, err := os.Stat(n.requester.recipientKeyPath(res.ExecID)); !os.IsNotExist(err) {
		t.Fatal("recipient key must be destroyed after decryption")
	}

	// Receipt: both statements verify and bind to each other.
	if err := exec.Completion.VerifySigners(protocol.SchemaCompletion, n.worker.PeerID().String()); err != nil {
		t.Fatal(err)
	}
	if err := exec.Acceptance.VerifySigners(protocol.SchemaAcceptance, n.requester.PeerID().String()); err != nil {
		t.Fatal(err)
	}
	var acc protocol.Acceptance
	exec.Acceptance.Decode(&acc)
	if acc.Verdict != protocol.VerdictAccepted || acc.ReceiptID != protocol.ReceiptID(exec.Completion) {
		t.Fatalf("acceptance %+v", acc)
	}
	if !wexec.Acceptance.SamePayload(exec.Acceptance) {
		t.Fatal("worker must hold the same acceptance")
	}

	// Parsed result on the requester matches the native parser and the digest.
	parsed, err := os.ReadFile(filepath.Join(res.ResultDir, "parsed", "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if contentid.BLAKE3(parsed) != acc.ParsedResultDigest {
		t.Fatal("parsed result digest mismatch")
	}
	var r qeparse.Result
	json.Unmarshal(parsed, &r)
	if !r.Converged || r.Energy == nil {
		t.Fatalf("parsed result %+v", r)
	}
	for _, f := range []string{"input/canonical-job.json", "output/stdout.txt", "provenance/lease.json", "provenance/receipt-completion.json", "provenance/receipt-acceptance.json", "provenance/execution-environment.json"} {
		if _, err := os.Stat(filepath.Join(res.ResultDir, f)); err != nil {
			t.Errorf("result record missing %s", f)
		}
	}

	// Both event chains verify.
	for _, a := range []*Agent{n.requester, n.worker} {
		if _, _, err := a.store.VerifyChain(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetachedFetch(t *testing.T) {
	n := setup(t)
	ctx := n.start(t)
	n.worker.SetMode(ModeAvailable)
	res, err := n.requester.Submit(ctx, SubmitRequest{JobPath: n.jobPath, Peer: n.worker.Addrs(), Detach: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Worker seals and holds ciphertext only.
	waitState(t, n.worker, res.ExecID, store.StateSealed, 60*time.Second)
	entries, _ := os.ReadDir(n.worker.paths.ExecDir(res.ExecID))
	for _, e := range entries {
		if e.Name() != "sealed" {
			t.Fatalf("worker holds %s after sealing; only sealed/ is allowed", e.Name())
		}
	}
	// The detached requester picks it up on its (shortened) fetch interval.
	waitState(t, n.requester, res.ExecID, store.StateCompleted, 120*time.Second)
}

func TestLeaseRejections(t *testing.T) {
	n := setup(t)
	ctx := n.start(t)
	n.worker.SetMode(ModeAvailable)

	// Untrusted solver signer on the worker side.
	n.worker.SetTrust(solver.Policy{TrustedSigners: []string{n.worker.PeerID().String()}})
	_, err := n.requester.Submit(ctx, SubmitRequest{JobPath: n.jobPath, Peer: n.worker.Addrs()}, nil)
	if err == nil || !strings.Contains(err.Error(), "trusted") {
		t.Fatalf("expected trust rejection, got %v", err)
	}
	n.worker.SetTrust(n.requester.Trust())

	// More CPU than offered.
	data, _ := os.ReadFile(n.jobPath)
	os.WriteFile(n.jobPath, []byte(strings.Replace(string(data), "cores: 1", "cores: 2", 1)), 0o644)
	if _, err := n.requester.Submit(ctx, SubmitRequest{JobPath: n.jobPath, Peer: n.worker.Addrs()}, nil); err == nil {
		t.Fatal("expected resource rejection")
	}
}

func TestDefaultConfigIsValid(t *testing.T) {
	c := config.Default(runtime.NumCPU(), 32<<30)
	if _, err := c.Policy(); err != nil {
		t.Fatal(err)
	}
}

func TestDHTDiscoveryWithoutPeer(t *testing.T) {
	n := setup(t)
	// A bootstrap/DHT server node that runs no jobs.
	signer := n.requester.Trust().TrustedSigners[0]
	noMDNS := func(c *config.Config) { c.Network.MDNS = false; c.Network.DHTMode = "server" }
	boot := newNode(t, signer, nil, noMDNS)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); boot.Close() })
	if err := boot.Start(ctx); err != nil {
		t.Fatal(err)
	}
	bootAddr := boot.Addrs()[0]

	// Rebuild requester and worker so their configs point at the bootstrap node.
	withBoot := func(c *config.Config) { noMDNS(c); c.Network.Bootstrap = []string{bootAddr} }
	worker := newNode(t, signer, n.fake, withBoot)
	requester := newNode(t, signer, nil, withBoot)
	for _, a := range []*Agent{worker, requester} {
		env, _ := solver.LoadFile(firstManifest(t, n.worker))
		if _, err := a.AddSolver(env); err != nil {
			t.Fatal(err)
		}
		if err := a.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { a.Close() })
	}
	if err := worker.SetMode(ModeAvailable); err != nil {
		t.Fatal(err)
	}

	// Provider records propagate through the bootstrap node.
	var res *SubmitResult
	var err error
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		res, err = requester.Submit(ctx, SubmitRequest{JobPath: n.jobPath}, nil)
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("submit without --peer: %v", err)
	}
	if res.Worker != worker.PeerID().String() {
		t.Fatalf("expected worker %s, got %s", worker.PeerID(), res.Worker)
	}
	waitState(t, requester, res.ExecID, store.StateCompleted, 120*time.Second)
}

func firstManifest(t *testing.T, a *Agent) string {
	t.Helper()
	entries, err := os.ReadDir(a.paths.Solvers())
	if err != nil || len(entries) == 0 {
		t.Fatal("no installed manifest")
	}
	return filepath.Join(a.paths.Solvers(), entries[0].Name())
}

func TestRevokedSolverIsRefused(t *testing.T) {
	n := setup(t)
	ctx := n.start(t)
	n.worker.SetMode(ModeAvailable)
	key, err := identity.Generate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Trust the revocation signer on the worker only.
	pol := n.worker.Trust()
	pol.TrustedSigners = append(pol.TrustedSigners, key.PeerID.String())
	n.worker.SetTrust(pol)
	next := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	env, err := solver.SignRevocations(&solver.RevocationList{
		Schema: solver.RevocationSchema, Namespace: config.DefaultNamespace, Sequence: 1,
		IssuedAt: next, NextUpdateBefore: next,
		Revoked: []solver.Revoked{{Kind: solver.RevokeManifest, Digest: n.manifestDigest, Reason: "broken_scientific_output"}},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.worker.InstallRevocations(env); err != nil {
		t.Fatal(err)
	}
	_, err = n.requester.Submit(ctx, SubmitRequest{JobPath: n.jobPath, Peer: n.worker.Addrs()}, nil)
	if err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("expected revocation rejection, got %v", err)
	}
	// Rollback protection: an older sequence is refused.
	old, _ := solver.SignRevocations(&solver.RevocationList{Schema: solver.RevocationSchema, Sequence: 1, NextUpdateBefore: next, Revoked: []solver.Revoked{}}, key)
	if _, err := n.worker.InstallRevocations(old); err == nil {
		// same sequence returns the installed list; it must still be the revoking one
		if n.worker.Revocations().Revoked[0].Digest != n.manifestDigest {
			t.Fatal("equal-sequence list must not replace the installed one")
		}
	}
}
