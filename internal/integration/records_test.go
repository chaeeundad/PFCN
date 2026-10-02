// Package integration runs multi-node scenarios through the public agent and
// indexer APIs (spec §58 demo, without containers).
package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chaeeundad/PFCN/internal/agent"
	"github.com/chaeeundad/PFCN/internal/config"
	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/identity"
	"github.com/chaeeundad/PFCN/internal/indexer"
	"github.com/chaeeundad/PFCN/internal/parser"
	"github.com/chaeeundad/PFCN/internal/record"
	"github.com/chaeeundad/PFCN/internal/sandbox"
	"github.com/chaeeundad/PFCN/internal/solver"
	"github.com/chaeeundad/PFCN/internal/store"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type cluster struct {
	t       *testing.T
	ctx     context.Context
	signer  *identity.Identity
	solver  []byte // signed manifest file
	digest  string
	outputs map[string][]byte
}

func newCluster(t *testing.T) *cluster {
	t.Helper()
	key, _ := identity.Generate(t.TempDir())
	m, err := solver.FromYAML([]byte(`schema: pumat.solver.v1
name: quantum-espresso
version: "7.4.1"
source: {repository: https://gitlab.com/QEF/q-e, commit: qe-7.4.1, license: GPL-2.0-or-later}
artifacts:
  - {platform: linux/arm64, reference: example.invalid/qe, mediaType: application/vnd.oci.image.manifest.v1+json, digest: sha256:4444444444444444444444444444444444444444444444444444444444444444}
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
	env, _ := solver.Sign(m, key)
	raw, _ := env.MarshalFile()
	xml, _ := os.ReadFile("../qeparse/testdata/data-file-schema.xml")
	out, _ := os.ReadFile("../qeparse/testdata/stdout.txt")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &cluster{t: t, ctx: ctx, signer: key, solver: raw, digest: env.SHA256(),
		outputs: map[string][]byte{"stdout.txt": out, "data-file-schema.xml": xml, "stderr.txt": nil}}
}

func (c *cluster) node(worker bool, tweak func(*config.Config)) *agent.Agent {
	t := c.t
	home, err := os.MkdirTemp("", "pumat-it-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	res, err := agent.Init(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg := res.Config
	cfg.Listen = []string{"/ip4/127.0.0.1/tcp/0"}
	cfg.Trust.SolverSigners = []string{c.signer.PeerID.String()}
	cfg.Resources.CPU, cfg.Resources.Memory = 1, "4GiB"
	cfg.Requester.ResultsDir = filepath.Join(home, "results")
	cfg.Network.MDNS = false
	cfg.Network.DHTMode = "server"
	if tweak != nil {
		tweak(cfg)
	}
	cfg.Save(agent.Paths{Home: home}.Config())
	var rt sandbox.Runtime
	if worker {
		rt = &sandbox.Fake{PlatformName: "linux/arm64", Outputs: c.outputs}
	}
	a, err := agent.Open(agent.Options{Home: home, Runtime: rt, Logger: quiet, FetchInterval: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := solverEnv(c.solver)
	if _, err := a.AddSolver(env); err != nil {
		t.Fatal(err)
	}
	if err := a.Start(c.ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	if worker {
		a.SetMode(agent.ModeAvailable)
	}
	return a
}

func (c *cluster) jobFile(publicJob bool) string {
	dir := c.t.TempDir()
	cif, _ := os.ReadFile("../../examples/qe-si-scf/silicon.cif")
	os.WriteFile(filepath.Join(dir, "silicon.cif"), cif, 0o644)
	upf := []byte("<UPF test pseudo>")
	os.WriteFile(filepath.Join(dir, "Si.upf"), upf, 0o644)
	vis := "public"
	if !publicJob {
		vis = "private"
	}
	p := filepath.Join(dir, "job.yaml")
	os.WriteFile(p, []byte(`apiVersion: pumat.org/v1alpha1
kind: QuantumEspressoJob
metadata: {name: si-scf, visibility: `+vis+`}
solver: {name: quantum-espresso, version: "7.4.1", manifestDigest: "`+c.digest+`", entrypoint: pw.x}
resources: {cpu: {cores: 1}, memory: 1GiB, walltime: 10m}
system:
  structure: {file: silicon.cif}
  pseudopotentials:
    Si: {filename: Si.upf, digest: "`+contentid.SHA256(upf)+`"}
calculation: {type: scf, parameters: {ecutwfc: 30, ecutrho: 240, kpoints: [4, 4, 4]}}
publication: {input: true, rawOutput: true, parsedOutput: true, provenance: true, license: CC-BY-4.0}
`), 0o644)
	return p
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func completed(a *agent.Agent, execID string) func() bool {
	return func() bool {
		e, err := a.Store().Get(execID)
		return err == nil && e.State == store.StateCompleted
	}
}

// TestPublishIndexReproduce follows the §58 demo: publish, index, reproduce on
// a different worker, and show REPRODUCED_* in the explorer.
func TestPublishIndexReproduce(t *testing.T) {
	c := newCluster(t)
	idxNode := c.node(false, nil)
	x, err := indexer.Open(idxNode, filepath.Join(idxNode.Home(), "index.sqlite"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	x.Start(c.ctx)
	idxAddr := idxNode.Addrs()[0]
	withIdx := func(cfg *config.Config) {
		cfg.Network.Bootstrap = []string{idxAddr}
		cfg.Publication.Indexers = []string{idxAddr}
	}
	w1 := c.node(true, withIdx)
	requester := c.node(false, withIdx)

	res, err := requester.Submit(c.ctx, agent.SubmitRequest{JobPath: c.jobFile(true), Peer: w1.Addrs()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "first execution", completed(requester, res.ExecID))

	pub, err := requester.Publish(c.ctx, res.ExecID)
	if err != nil {
		t.Fatal(err)
	}
	if pub.Announced != 1 {
		t.Fatalf("expected push to 1 indexer, got %d", pub.Announced)
	}
	waitFor(t, 30*time.Second, "indexer to index the record", func() bool {
		rows, _ := x.Search(indexer.Query{Formula: "Si"})
		return len(rows) == 1
	})

	// Offline verification of the published bundle.
	env, m, err := requester.LocalRecord(pub.RecordID)
	if err != nil || record.ID(env) != pub.RecordID {
		t.Fatal(err)
	}
	if err := record.VerifyFiles(m, pub.Dir); err != nil {
		t.Fatal(err)
	}

	// Reproduce on a second worker from a fresh node; the original worker is excluded.
	w2 := c.node(true, withIdx)
	_ = w2
	reproducer := c.node(false, withIdx)
	var rres *agent.SubmitResult
	waitFor(t, 30*time.Second, "reproduction lease", func() bool {
		rres, err = reproducer.Reproduce(c.ctx, agent.ReproduceRequest{RecordID: pub.RecordID}, nil)
		return err == nil
	})
	if rres.Worker == w1.PeerID().String() {
		t.Fatal("reproduction must run on a different worker")
	}
	waitFor(t, 60*time.Second, "reproduction", completed(reproducer, rres.ExecID))
	raw, err := os.ReadFile(filepath.Join(rres.ResultDir, "reproduction.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cmp record.Comparison
	json.Unmarshal(raw, &cmp)
	if cmp.Status != record.StatusReproducedExact || cmp.Against != pub.RecordID {
		t.Fatalf("comparison %+v", cmp)
	}
	rpub, err := reproducer.Publish(c.ctx, rres.ExecID)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "reproducibility status", func() bool {
		r, _ := x.Reproducibility(m.CalcID)
		return r.Executions == 2 && r.Status == record.StatusReproducedExact
	})

	// Explorer pages and API.
	srv := httptest.NewServer(x.Handler())
	defer srv.Close()
	for _, path := range []string{"/", "/?formula=Si", "/record/" + rpub.RecordID, "/api/records?element=Si", "/record/" + pub.RecordID + "/files/parsed/result.json"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", path, err, resp.Status)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if path == "/record/"+rpub.RecordID && !strings.Contains(string(body), record.StatusReproducedExact) {
			t.Fatal("record page must show the reproducibility status")
		}
	}
	if resp, _ := http.Get(srv.URL + "/record/" + pub.RecordID + "/files/../../etc/passwd"); resp != nil && resp.StatusCode == 200 {
		t.Fatal("path traversal must be refused")
	}

	// The index is disposable: rebuilding reproduces the same searchable records.
	before, _ := x.Search(indexer.Query{})
	n, err := x.Rebuild()
	if err != nil || n != len(before) {
		t.Fatalf("rebuild indexed %d, had %d (%v)", n, len(before), err)
	}
	after, _ := x.Search(indexer.Query{})
	if len(after) != len(before) {
		t.Fatal("rebuild changed the searchable records")
	}
}

func TestPrivateJobsAreNeverPublished(t *testing.T) {
	c := newCluster(t)
	w := c.node(true, nil)
	r := c.node(false, nil)
	res, err := r.Submit(c.ctx, agent.SubmitRequest{JobPath: c.jobFile(false), Peer: w.Addrs()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "execution", completed(r, res.ExecID))
	if _, err := r.Publish(c.ctx, res.ExecID); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("expected private refusal, got %v", err)
	}
}

func solverEnv(raw []byte) (*envelope.Envelope, error) { return envelope.ParseFile(raw) }
