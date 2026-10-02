package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/protocol"
	"github.com/chaeeundad/PFCN/internal/store"
)

// Local API (§32.0): the CLI talks to the agent over a Unix domain socket.
// Only processes that can open the socket (mode 0660) can control the node.

// StatusView is returned by GET /v1/status.
type StatusView struct {
	PeerID      string   `json:"peer_id"`
	Addrs       []string `json:"addrs"`
	Mode        string   `json:"mode"`
	Namespace   string   `json:"namespace"`
	Runtime     string   `json:"runtime"`
	Platform    string   `json:"platform"`
	CPUModel    string   `json:"cpu_model"`
	CPUOffered  int64    `json:"cpu_offered"`
	MemOffered  int64    `json:"memory_offered_bytes"`
	RunningJobs int      `json:"running_jobs"`
	Peers       int      `json:"connected_peers"`
}

// JobView is the API form of an execution.
type JobView struct {
	ExecID          string `json:"exec_id"`
	Role            string `json:"role"`
	State           string `json:"state"`
	CalcID          string `json:"calc_id"`
	Peer            string `json:"peer"`
	JobName         string `json:"job_name"`
	Mode            string `json:"mode"`
	ResultDir       string `json:"result_dir,omitempty"`
	Error           string `json:"error,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	LastConfirmedAt string `json:"last_confirmed_at,omitempty"`
	ElapsedSeconds  int64  `json:"elapsed_seconds,omitempty"`
	ReceiptID       string `json:"receipt_id,omitempty"`
	Outcome         string `json:"outcome,omitempty"`
	Verdict         string `json:"verdict,omitempty"`
	RetentionUntil  string `json:"retention_until,omitempty"`
}

func viewOf(e *store.Execution) JobView {
	v := JobView{
		ExecID: e.ExecID, Role: e.Role, State: e.State, CalcID: e.CalcID, Peer: e.PeerID,
		JobName: e.JobName, Mode: e.Mode, ResultDir: e.ResultDir, Error: e.Error,
		CreatedAt: protocol.Now(e.CreatedAt), UpdatedAt: protocol.Now(e.UpdatedAt), ElapsedSeconds: e.ElapsedSeconds,
	}
	if e.Role == store.RoleWorker {
		v.ResultDir = ""
	}
	if !e.LastConfirmedAt.IsZero() {
		v.LastConfirmedAt = protocol.Now(e.LastConfirmedAt)
	}
	if !e.RetentionDeadline.IsZero() {
		v.RetentionUntil = protocol.Now(e.RetentionDeadline)
	}
	if e.Completion != nil {
		v.ReceiptID = protocol.ReceiptID(e.Completion)
		var c protocol.Completion
		if e.Completion.Decode(&c) == nil {
			v.Outcome = c.Outcome
		}
	}
	if e.Acceptance != nil {
		var a protocol.Acceptance
		if e.Acceptance.Decode(&a) == nil {
			v.Verdict = a.Verdict
		}
	}
	return v
}

// ReceiptView carries the signed receipt statements of one execution.
type ReceiptView struct {
	ExecID     string             `json:"exec_id"`
	ReceiptID  string             `json:"receipt_id"`
	Lease      *envelope.Envelope `json:"lease"`
	Completion *envelope.Envelope `json:"completion"`
	Acceptance *envelope.Envelope `json:"acceptance,omitempty"`
}

// ServeAPI listens on the agent socket until ctx is done.
func (a *Agent) ServeAPI(ctx context.Context) error {
	sock := a.paths.Socket()
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	os.Chmod(sock, 0o660)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", a.apiStatus)
	mux.HandleFunc("POST /v1/mode", a.apiMode)
	mux.HandleFunc("POST /v1/submit", a.apiSubmit)
	mux.HandleFunc("GET /v1/jobs", a.apiJobs)
	mux.HandleFunc("GET /v1/jobs/{id}", a.apiJob)
	mux.HandleFunc("POST /v1/jobs/{id}/fetch", a.apiFetch)
	mux.HandleFunc("GET /v1/receipts/{id}", a.apiReceipt)
	mux.HandleFunc("POST /v1/solvers", a.apiAddSolver)
	mux.HandleFunc("GET /v1/solvers", a.apiSolvers)
	mux.HandleFunc("GET /v1/ledger/verify", a.apiLedger)
	mux.HandleFunc("GET /v1/capability", a.apiCapability)
	mux.HandleFunc("POST /v1/revocations", a.apiAddRevocations)
	mux.HandleFunc("GET /v1/revocations", func(w http.ResponseWriter, _ *http.Request) {
		rl := a.Revocations()
		if rl == nil {
			writeErr(w, 404, errors.New("no revocation list installed"))
			return
		}
		writeJSON(w, 200, rl)
	})
	mux.HandleFunc("GET /v1/network", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, a.Network()) })
	mux.HandleFunc("POST /v1/jobs/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		res, err := a.Publish(r.Context(), r.PathValue("id"))
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, 200, res)
	})
	mux.HandleFunc("POST /v1/reproduce", a.apiReproduce)
	mux.HandleFunc("GET /v1/reputation", func(w http.ResponseWriter, _ *http.Request) {
		stats, err := a.store.Stats("")
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		out := []*store.PeerStats{}
		for _, s := range stats {
			out = append(out, s)
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("POST /v1/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Cancel(r.Context(), r.PathValue("id")); err != nil {
			writeErr(w, 409, err)
			return
		}
		writeJSON(w, 200, map[string]string{"state": store.StateCancelled})
	})
	mux.HandleFunc("POST /v1/records/{id}/fetch", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
		defer cancel()
		m, err := a.FetchRecord(ctx, r.PathValue("id"))
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		dir, _ := a.RecordDir(r.PathValue("id"))
		writeJSON(w, 200, map[string]any{"manifest": m, "dir": dir})
	})
	a.mu.Lock()
	for _, reg := range a.extraAPI {
		reg(mux)
	}
	a.mu.Unlock()
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
		os.Remove(sock)
	}()
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (a *Agent) apiStatus(w http.ResponseWriter, _ *http.Request) {
	n, _ := a.activeWorkerJobs()
	writeJSON(w, 200, StatusView{
		PeerID: a.id.PeerID.String(), Addrs: a.Addrs(), Mode: a.Mode(), Namespace: a.cfg.Namespace,
		Runtime: a.runtimeName(), Platform: a.platform, CPUModel: a.hw.CPUModel,
		CPUOffered: a.policy.CPUCores, MemOffered: a.policy.MemoryBytes, RunningJobs: n,
		Peers: len(a.host.Network().Peers()),
	})
}

func (a *Agent) apiMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := a.SetMode(body.Mode); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]string{"mode": a.Mode()})
}

// RegisterAPI adds handlers to the local API (call before ServeAPI).
func (a *Agent) RegisterAPI(fn func(*http.ServeMux)) {
	a.mu.Lock()
	a.extraAPI = append(a.extraAPI, fn)
	a.mu.Unlock()
}

func (a *Agent) apiReproduce(w http.ResponseWriter, r *http.Request) {
	var req ReproduceRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	res, err := a.Reproduce(r.Context(), req, func(s string) {
		enc.Encode(map[string]string{"progress": s})
		flush()
	})
	if err != nil {
		enc.Encode(map[string]string{"error": err.Error()})
		return
	}
	enc.Encode(map[string]any{"result": res})
	flush()
}

// apiSubmit streams progress as NDJSON: {"progress": "..."} lines, then a
// final {"result": {...}} or {"error": "..."}.
func (a *Agent) apiSubmit(w http.ResponseWriter, r *http.Request) {
	var req SubmitRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	res, err := a.Submit(r.Context(), req, func(s string) {
		enc.Encode(map[string]string{"progress": s})
		flush()
	})
	if err != nil {
		enc.Encode(map[string]string{"error": err.Error()})
		return
	}
	enc.Encode(map[string]any{"result": res})
	flush()
}

func (a *Agent) apiJobs(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.List(r.URL.Query().Get("role"), r.URL.Query().Get("pending") == "1")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	out := []JobView{}
	for _, e := range list {
		out = append(out, viewOf(e))
	}
	writeJSON(w, 200, out)
}

func (a *Agent) apiJob(w http.ResponseWriter, r *http.Request) {
	e, err := a.store.Find(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, viewOf(e))
}

func (a *Agent) apiFetch(w http.ResponseWriter, r *http.Request) {
	e, err := a.store.Find(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	state, ferr := a.Fetch(ctx, e.ExecID)
	e, _ = a.store.Get(e.ExecID)
	v := viewOf(e)
	resp := map[string]any{"job": v, "state": state}
	if ferr != nil {
		resp["fetch_error"] = ferr.Error()
	}
	writeJSON(w, 200, resp)
}

func (a *Agent) apiReceipt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	match := func(full, kind string) bool {
		if !strings.Contains(id, ":") {
			return strings.HasPrefix(full, "pumat:"+kind+":blake3:"+id)
		}
		return strings.HasPrefix(full, id)
	}
	list, err := a.store.List("", false)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	for _, e := range list {
		if e.Completion == nil {
			continue
		}
		rid := protocol.ReceiptID(e.Completion)
		if match(rid, "receipt") || match(e.ExecID, "exec") {
			writeJSON(w, 200, ReceiptView{ExecID: e.ExecID, ReceiptID: rid, Lease: e.Lease, Completion: e.Completion, Acceptance: e.Acceptance})
			return
		}
	}
	writeErr(w, 404, errors.New("receipt not found"))
}

func (a *Agent) apiAddSolver(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	env, err := envelope.ParseFile(raw)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	v, err := a.AddSolver(env)
	if err != nil {
		writeErr(w, 422, err)
		return
	}
	writeJSON(w, 200, map[string]string{"name": v.Manifest.Name, "version": v.Manifest.Version, "digest": v.Digest})
}

func (a *Agent) apiSolvers(w http.ResponseWriter, _ *http.Request) {
	cat, err := a.solverCatalog()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	type sv struct {
		Name      string   `json:"name"`
		Version   string   `json:"version"`
		Digest    string   `json:"digest"`
		Platforms []string `json:"platforms"`
		Cached    bool     `json:"cached"`
	}
	out := []sv{}
	for d, v := range cat {
		s := sv{Name: v.Manifest.Name, Version: v.Manifest.Version, Digest: d}
		for _, art := range v.Manifest.Artifacts {
			s.Platforms = append(s.Platforms, art.Platform)
			if art.Platform == a.platform && a.rt != nil {
				s.Cached = a.rt.HasImage(context.Background(), art.Reference+"@"+art.Digest)
			}
		}
		out = append(out, s)
	}
	writeJSON(w, 200, out)
}

func (a *Agent) apiLedger(w http.ResponseWriter, _ *http.Request) {
	n, head, err := a.store.VerifyChain()
	resp := map[string]any{"events": n, "head": head, "ok": err == nil}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, 200, resp)
}

func (a *Agent) apiCapability(w http.ResponseWriter, _ *http.Request) {
	env, err := a.Capability()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	var c protocol.Capability
	env.Decode(&c)
	writeJSON(w, 200, c)
}

func (a *Agent) apiAddRevocations(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	env, err := envelope.ParseFile(raw)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	rl, err := a.InstallRevocations(env)
	if err != nil {
		writeErr(w, 422, err)
		return
	}
	writeJSON(w, 200, rl)
}
