package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/chaeeundad/PFCN/internal/bundle"
	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/job"
	"github.com/chaeeundad/PFCN/internal/parser"
	"github.com/chaeeundad/PFCN/internal/pb"
	"github.com/chaeeundad/PFCN/internal/protocol"
	"github.com/chaeeundad/PFCN/internal/seal"
	"github.com/chaeeundad/PFCN/internal/solver"
	"github.com/chaeeundad/PFCN/internal/store"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

// SubmitRequest is a job submission from the local CLI.
type SubmitRequest struct {
	JobPath   string   `json:"job_path"`
	Peer      []string `json:"peer"`      // worker multiaddrs ending in /p2p/<id>
	Detach    bool     `json:"detach"`    // overrides delivery.mode
	Retention string   `json:"retention"` // overrides delivery.resultRetention
}

// SubmitResult describes an accepted submission.
type SubmitResult struct {
	ExecID    string `json:"exec_id"`
	CalcID    string `json:"calc_id"`
	LeaseID   string `json:"lease_id"`
	Worker    string `json:"worker"`
	Mode      string `json:"mode"`
	ResultDir string `json:"result_dir"`
}

// Submit validates a job, leases a worker, and uploads the input bundle from
// local disk (§5.5, §16.7). It returns once the worker is RUNNING.
func (a *Agent) Submit(ctx context.Context, req SubmitRequest, progress func(string)) (*SubmitResult, error) {
	if progress == nil {
		progress = func(string) {}
	}
	data, err := os.ReadFile(req.JobPath)
	if err != nil {
		return nil, err
	}
	spec, err := job.ParseYAML(data)
	if err != nil {
		return nil, err
	}
	if req.Detach {
		spec.Delivery.Mode = job.DeliveryDetached
	}
	if req.Retention != "" {
		spec.Delivery.ResultRetention = req.Retention
	}
	prep, err := job.Prepare(spec, filepath.Dir(req.JobPath), a.reg, a.cfg.Requester.PseudoDirs)
	if err != nil {
		return nil, err
	}
	cj := &prep.Canonical
	progress("Validating job specification... OK")

	sv, err := a.manifestFor(cj.Solver.ManifestDigest)
	if err != nil {
		return nil, fmt.Errorf("%w (install it with `pumat solver add <manifest.signed.json>`)", err)
	}
	if err := a.checkRevocation(sv); err != nil {
		return nil, err
	}
	if sv.Manifest.Name != cj.Solver.Name || sv.Manifest.Version != cj.Solver.Version {
		return nil, errors.New("job solver name/version does not match the pinned manifest")
	}
	if _, ok := parser.Builtin(sv.Manifest.Parser.Digest); !ok {
		return nil, fmt.Errorf("parser %s is not available in this agent build", sv.Manifest.Parser.Digest)
	}
	calcID, err := cj.CalcID()
	if err != nil {
		return nil, err
	}
	progress(fmt.Sprintf("Solver: %s %s", sv.Manifest.Name, sv.Manifest.Version))
	progress("Solver manifest: " + sv.Digest)
	progress("Visibility: " + cj.Metadata.Visibility)
	progress("Calculation: " + calcID)

	// Input bundle (§16.1): canonical job + pinned files, from local disk.
	cjBytes, err := cj.Marshal()
	if err != nil {
		return nil, err
	}
	files := []bundle.File{{Name: "input/canonical-job.json", Data: cjBytes}}
	for name, d := range prep.Files {
		files = append(files, bundle.File{Name: "files/" + name, Data: d})
	}
	tarData, err := bundle.Build(files)
	if err != nil {
		return nil, err
	}
	inDigest := bundle.Digest(tarData, bundle.ChunkSize)

	var cands []candidate
	if len(req.Peer) > 0 {
		worker, err := a.connect(ctx, req.Peer)
		if err != nil {
			return nil, err
		}
		capDoc, err := a.QueryCapability(ctx, worker)
		if err != nil {
			return nil, fmt.Errorf("capability query: %w", err)
		}
		if capDoc.Status != ModeAvailable {
			return nil, fmt.Errorf("worker %s is %s", worker, capDoc.Status)
		}
		if _, ok := sv.Manifest.ArtifactFor(capDoc.Platform); !ok {
			return nil, fmt.Errorf("solver has no artifact for worker platform %s", capDoc.Platform)
		}
		if capDoc.CPU.AvailableCores < cj.Resources.CPUCores || capDoc.MemoryBytes < cj.Resources.MemoryBytes {
			return nil, errors.New("worker does not offer enough CPU or memory for this job")
		}
		cands = []candidate{{id: worker, cap: capDoc}}
	} else {
		progress("Discovering compatible workers...")
		found, err := a.findCandidates(ctx, sv, cj.Resources.CPUCores, cj.Resources.MemoryBytes)
		if err != nil {
			return nil, err
		}
		progress(fmt.Sprintf("%d currently eligible", len(found)))
		cands = found
	}

	ls := leaseSpec{cj: cj, calcID: calcID, sv: sv, inDigest: inDigest, inSize: int64(len(tarData))}
	var execID string
	var worker peer.ID
	var leaseEnv *envelope.Envelope
	for i, cand := range cands {
		progress(fmt.Sprintf("Worker %s: %s, %d cores available", shortPeer(cand.id), cand.cap.Platform, cand.cap.CPU.AvailableCores))
		addrs := req.Peer
		if len(addrs) == 0 {
			addrs = a.peerAddrs(cand.id)
		}
		execID, leaseEnv, err = a.leaseWith(ctx, cand.id, addrs, ls)
		if err == nil {
			worker = cand.id
			break
		}
		if i == len(cands)-1 {
			return nil, err
		}
		progress(fmt.Sprintf("Peer %s declined (%v); trying the next candidate", shortPeer(cand.id), err))
	}
	leaseID := protocol.LeaseID(leaseEnv)
	resultDir := filepath.Join(a.cfg.Requester.ResultsDir, execDirName(execID))
	progress("Lease accepted by peer " + shortPeer(worker))
	progress("Execution: " + execID)

	// Keep the submitted input locally as part of the result record (§22.2).
	if err := writeInputRecord(resultDir, cjBytes, data, prep.Files); err != nil {
		return nil, err
	}
	dir := a.paths.ExecDir(execID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "input.bundle"), tarData, 0o600); err != nil {
		return nil, err
	}
	progress("Uploading encrypted input from local disk...")
	if err := a.uploadInput(ctx, execID); err != nil {
		return nil, fmt.Errorf("input upload interrupted (the agent retries until the input deadline): %w", err)
	}
	progress("Upload done. Worker is running the job.")
	a.nudge()
	return &SubmitResult{ExecID: execID, CalcID: calcID, LeaseID: leaseID, Worker: worker.String(), Mode: cj.Delivery.Mode, ResultDir: resultDir}, nil
}

// leaseSpec is the job-specific part of a lease request.
type leaseSpec struct {
	cj       *job.Canonical
	calcID   string
	sv       *solver.Verified
	inDigest string
	inSize   int64
}

// leaseWith creates an execution for one worker and negotiates its lease.
func (a *Agent) leaseWith(ctx context.Context, worker peer.ID, addrs []string, ls leaseSpec) (string, *envelope.Envelope, error) {
	cj := ls.cj
	// Per-execution recipient key, persisted before it is ever used (§15.3).
	rk, err := seal.NewRecipientKey()
	if err != nil {
		return "", nil, err
	}
	nonce := randomHex(16)
	execID, err := protocol.ExecID(ls.calcID, a.id.PeerID.String(), worker.String(), nonce)
	if err != nil {
		return "", nil, err
	}
	if err := a.saveRecipientKey(execID, rk); err != nil {
		return "", nil, err
	}
	lr := protocol.LeaseRequest{
		Schema: protocol.SchemaLeaseRequest, Namespace: a.cfg.Namespace,
		ExecID: execID, CalcID: ls.calcID, RequesterPeerID: a.id.PeerID.String(), WorkerPeerID: worker.String(),
		RequesterNonce: nonce, SolverManifestDigest: ls.sv.Digest, Entrypoint: cj.Solver.Entrypoint,
		Visibility: cj.Metadata.Visibility,
		Resources: protocol.Resources{
			CPUMillicores: cj.Resources.CPUCores * 1000, MemoryBytes: cj.Resources.MemoryBytes,
		},
		MaxWalltimeSeconds: cj.Resources.WalltimeSeconds,
		InputBundleDigest:  ls.inDigest, InputBundleSize: ls.inSize,
		ResultRetentionSeconds: cj.Delivery.ResultRetentionSeconds,
		ResultRecipientKey:     rk.Public(),
		IssuedAt:               protocol.Now(time.Now()),
	}
	lrEnv, err := envelope.Sign(a.id, protocol.SchemaLeaseRequest, lr)
	if err != nil {
		return "", nil, err
	}
	exec := &store.Execution{
		ExecID: execID, Role: store.RoleRequester, State: store.StateRequested, CalcID: ls.calcID,
		PeerID: worker.String(), PeerAddrs: addrs, JobName: cj.Metadata.Name, Mode: cj.Delivery.Mode,
		ResultDir: filepath.Join(a.cfg.Requester.ResultsDir, execDirName(execID)),
	}
	if err := a.store.Insert(exec, "lease_request", nil); err != nil {
		return "", nil, err
	}
	leaseEnv, err := a.negotiateLease(ctx, worker, lrEnv, &lr, ls.sv)
	if err != nil {
		msg := err.Error()
		a.store.Transition(execID, []string{store.StateRequested}, store.StateRejected, store.Update{Error: &msg}, nil)
		os.Remove(a.recipientKeyPath(execID))
		return "", nil, err
	}
	return execID, leaseEnv, nil
}

func (a *Agent) negotiateLease(ctx context.Context, worker peer.ID, lrEnv *envelope.Envelope, lr *protocol.LeaseRequest, sv *solver.Verified) (*envelope.Envelope, error) {
	s, err := a.host.NewStream(ctx, worker, protocol.ProtoLease)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	s.SetDeadline(time.Now().Add(60 * time.Second))
	c := protocol.NewConn(s)
	if err := c.Send(&pb.Frame{Body: &pb.Frame_LeaseRequest{LeaseRequest: &pb.LeaseRequest{
		Request: protocol.ToPB(lrEnv), SolverManifest: protocol.ToPB(sv.Envelope),
	}}}); err != nil {
		return nil, err
	}
	f, err := c.Recv()
	if err != nil {
		return nil, err
	}
	offer, err := protocol.FromPB(f.GetLeaseOffer())
	if err != nil {
		return nil, err
	}
	if err := offer.VerifySigners(protocol.SchemaLease, worker.String()); err != nil {
		return nil, err
	}
	var l protocol.Lease
	if err := offer.Decode(&l); err != nil {
		return nil, err
	}
	art, ok := sv.Manifest.ArtifactFor(l.Platform)
	switch {
	case l.ExecID != lr.ExecID || l.CalcID != lr.CalcID || l.Namespace != lr.Namespace:
		return nil, errors.New("lease offer does not match the request identity")
	case l.RequesterPeerID != lr.RequesterPeerID || l.WorkerPeerID != lr.WorkerPeerID || l.RequesterNonce != lr.RequesterNonce:
		return nil, errors.New("lease offer has wrong peers or nonce")
	case l.SolverManifestDigest != lr.SolverManifestDigest || l.Entrypoint != lr.Entrypoint || !ok || art.Digest != l.ArtifactDigest:
		return nil, errors.New("lease offer has a different solver artifact")
	case l.Resources != lr.Resources || l.MaxWalltimeSeconds != lr.MaxWalltimeSeconds || l.Visibility != lr.Visibility:
		return nil, errors.New("lease offer changes resources or visibility")
	case l.InputBundleDigest != lr.InputBundleDigest || l.InputBundleSize != lr.InputBundleSize:
		return nil, errors.New("lease offer has a different input bundle")
	case l.ResultRecipientKey != lr.ResultRecipientKey || l.CustodianPeerID != lr.CustodianPeerID:
		return nil, errors.New("lease offer changes result delivery")
	case l.ResultRetentionSeconds <= 0 || l.ResultRetentionSeconds > lr.ResultRetentionSeconds:
		return nil, errors.New("lease offer has an invalid retention")
	}
	if err := protocol.CheckIssued(l.IssuedAt, time.Now()); err != nil {
		return nil, err
	}
	offer.AddSignature(a.id)
	deadline, _ := protocol.ParseTime(l.InputDeadline)
	// Persist LEASED before sending the countersignature (§15.2).
	if err := a.store.Transition(lr.ExecID, []string{store.StateRequested}, store.StateLeased,
		store.Update{Lease: offer, InputDeadline: &deadline}, map[string]any{"lease_id": protocol.LeaseID(offer)}); err != nil {
		return nil, err
	}
	if err := c.Send(&pb.Frame{Body: &pb.Frame_LeaseCountersigned{LeaseCountersigned: protocol.ToPB(offer)}}); err != nil {
		return nil, err
	}
	f, err = c.Recv()
	if err != nil {
		return nil, err
	}
	if f.GetLeaseConfirm() == nil || f.GetLeaseConfirm().LeaseId != protocol.LeaseID(offer) {
		return nil, errors.New("worker did not confirm the lease")
	}
	return offer, nil
}

// uploadInput sends missing input chunks; it is safe to call repeatedly.
func (a *Agent) uploadInput(ctx context.Context, execID string) error {
	exec, err := a.store.Get(execID)
	if err != nil {
		return err
	}
	if exec.State != store.StateLeased && exec.State != store.StateInputTransfer {
		return nil
	}
	path := filepath.Join(a.paths.ExecDir(execID), "input.bundle")
	leaves, size, _, err := bundle.FileDigest(path, bundle.ChunkSize)
	if err != nil {
		return err
	}
	worker, err := a.connect(ctx, exec.PeerAddrs)
	if err != nil {
		return err
	}
	if exec.State == store.StateLeased {
		if err := a.store.Transition(execID, []string{store.StateLeased}, store.StateInputTransfer, store.Update{}, nil); err != nil {
			return err
		}
	}
	s, err := a.host.NewStream(ctx, worker, protocol.ProtoInput)
	if err != nil {
		return err
	}
	defer s.Close()
	c := protocol.NewConn(s)
	begin := &pb.InputBegin{ExecId: execID, Size: size, ChunkSize: bundle.ChunkSize}
	for _, l := range leaves {
		begin.ChunkHashes = append(begin.ChunkHashes, append([]byte(nil), l[:]...))
	}
	s.SetDeadline(time.Now().Add(60 * time.Second))
	if err := c.Send(&pb.Frame{Body: &pb.Frame_InputBegin{InputBegin: begin}}); err != nil {
		return err
	}
	f, err := c.Recv()
	if err != nil {
		return err
	}
	have := f.GetInputHave()
	if have == nil {
		return errors.New("worker did not report input state")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	buf := make([]byte, bundle.ChunkSize)
	for _, idx := range have.Missing {
		off := int64(idx) * bundle.ChunkSize
		n, err := file.ReadAt(buf[:min(int64(bundle.ChunkSize), size-off)], off)
		if err != nil && n == 0 {
			return err
		}
		s.SetDeadline(time.Now().Add(2 * time.Minute))
		if err := c.Send(&pb.Frame{Body: &pb.Frame_Chunk{Chunk: &pb.Chunk{Index: idx, Data: buf[:n]}}}); err != nil {
			return err
		}
	}
	s.SetDeadline(time.Now().Add(5 * time.Minute))
	f, err = c.Recv()
	if err != nil {
		return err
	}
	if f.GetInputDone() == nil || !f.GetInputDone().Ok {
		return errors.New("worker did not confirm the input")
	}
	if err := a.store.Transition(execID, []string{store.StateInputTransfer}, store.StateRunning, store.Update{}, nil); err != nil {
		return err
	}
	os.Remove(path)
	return nil
}

// --- fetch loop (§16.8) -----------------------------------------------------

func (a *Agent) fetchLoop(ctx context.Context) {
	defer a.wg.Done()
	lastFull := time.Time{}
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.wake:
		}
		full := time.Since(lastFull) >= a.fetchInterval
		if full {
			lastFull = time.Now()
		}
		list, err := a.store.List(store.RoleRequester, true)
		if err != nil {
			continue
		}
		for _, e := range list {
			// Attached jobs are polled every few seconds; detached ones on
			// the slow interval or when nudged by `pumat job fetch`.
			if e.State == store.StateRequested || (!full && e.Mode == job.DeliveryDetached) {
				continue
			}
			a.mu.Lock()
			busy := a.fetching[e.ExecID]
			a.mu.Unlock()
			if busy {
				continue
			}
			go func(id string) {
				fctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
				defer cancel()
				if _, err := a.Fetch(fctx, id); err != nil {
					a.log.Info("fetch attempt failed (will retry)", "exec", id, "err", err)
				}
			}(e.ExecID)
		}
	}
}

// Fetch performs one fetch attempt for a requester execution and returns its
// resulting local state.
func (a *Agent) Fetch(ctx context.Context, execID string) (string, error) {
	a.mu.Lock()
	if a.fetching[execID] {
		a.mu.Unlock()
		return "", errors.New("fetch already in progress")
	}
	a.fetching[execID] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.fetching, execID)
		a.mu.Unlock()
	}()

	exec, err := a.store.Get(execID)
	if err != nil {
		return "", err
	}
	if exec.Role != store.RoleRequester {
		return "", errors.New("not a requester execution")
	}
	if store.Terminal(exec.State) {
		return exec.State, nil
	}
	if exec.State == store.StateLeased || exec.State == store.StateInputTransfer {
		if err := a.uploadInput(ctx, execID); err != nil {
			return a.markLostIfPastDeadline(exec, err)
		}
		return store.StateRunning, nil
	}
	worker, err := a.connect(ctx, exec.PeerAddrs)
	if err != nil {
		return a.markLostIfPastDeadline(exec, err)
	}
	s, err := a.host.NewStream(ctx, worker, protocol.ProtoResult)
	if err != nil {
		return a.markLostIfPastDeadline(exec, err)
	}
	defer s.Close()
	c := protocol.NewConn(s)
	s.SetDeadline(time.Now().Add(60 * time.Second))
	if err := c.Send(&pb.Frame{Body: &pb.Frame_StatusRequest{StatusRequest: &pb.StatusRequest{ExecId: execID}}}); err != nil {
		return "", err
	}
	f, err := c.Recv()
	if protocol.IsRemote(err, protocol.ErrCodeNotFound) {
		msg := "worker no longer knows this execution"
		a.store.Transition(execID, nil, store.StateWorkerLost, store.Update{Error: &msg}, nil)
		return store.StateWorkerLost, nil
	}
	if err != nil {
		return "", err
	}
	st := f.GetStatus()
	if st == nil {
		return "", errors.New("expected status")
	}
	a.store.Touch(execID, time.Now(), st.ElapsedSeconds)

	switch st.State {
	case store.StateLeased, store.StateInputTransfer, store.StateRunning:
		return exec.State, nil
	case store.StateFailed, store.StateExpired:
		msg := "worker reported " + st.State + ": " + st.Message
		a.store.Transition(execID, nil, store.StateFailed, store.Update{Error: &msg}, nil)
		os.Remove(a.recipientKeyPath(execID))
		return store.StateFailed, nil
	case store.StateResultExpired:
		msg := "the worker deleted the sealed result after its retention deadline"
		a.store.Transition(execID, nil, store.StateResultExpired, store.Update{Error: &msg}, nil)
		os.Remove(a.recipientKeyPath(execID))
		return store.StateResultExpired, nil
	case store.StateCompleted:
		if exec.State == store.StateAcknowledged {
			a.store.Transition(execID, []string{store.StateAcknowledged}, store.StateCompleted, store.Update{}, nil)
			return store.StateCompleted, nil
		}
		return exec.State, errors.New("worker completed an execution this node never acknowledged")
	case store.StateSealed:
	default:
		return exec.State, fmt.Errorf("unexpected worker state %q", st.State)
	}

	if exec.State == store.StateAcknowledged {
		return a.sendAck(c, exec)
	}
	comp, compEnv, err := a.verifyCompletion(exec, st, worker)
	if err != nil {
		return exec.State, err
	}
	if exec.State != store.StateSealed {
		if err := a.store.Transition(execID, nil, store.StateSealed, store.Update{Completion: compEnv}, map[string]any{"receipt_id": protocol.ReceiptID(compEnv)}); err != nil {
			return "", err
		}
	}
	if err := a.downloadSealed(c, s.SetDeadline, execID, st); err != nil {
		return store.StateSealed, err
	}
	if err := a.finalize(ctx, execID, comp, compEnv, st.Enc); err != nil {
		return store.StateSealed, err
	}
	exec, err = a.store.Get(execID)
	if err != nil {
		return "", err
	}
	return a.sendAck(c, exec)
}

func (a *Agent) markLostIfPastDeadline(exec *store.Execution, cause error) (string, error) {
	var lease protocol.Lease
	if exec.Lease == nil || exec.Lease.Decode(&lease) != nil {
		return exec.State, cause
	}
	issued, _ := protocol.ParseTime(lease.IssuedAt)
	limit := issued.Add(time.Duration(lease.MaxWalltimeSeconds+lease.ResultRetentionSeconds)*time.Second + 2*minInputWindow)
	if time.Now().After(limit) {
		msg := "worker unreachable past the result retention deadline: " + cause.Error()
		a.store.Transition(exec.ExecID, nil, store.StateWorkerLost, store.Update{Error: &msg}, nil)
		os.Remove(a.recipientKeyPath(exec.ExecID))
		return store.StateWorkerLost, nil
	}
	return exec.State, cause
}

func (a *Agent) verifyCompletion(exec *store.Execution, st *pb.Status, worker peer.ID) (*protocol.Completion, *envelope.Envelope, error) {
	env, err := protocol.FromPB(st.Completion)
	if err != nil {
		return nil, nil, err
	}
	if err := env.VerifySigners(protocol.SchemaCompletion, worker.String()); err != nil {
		return nil, nil, err
	}
	var comp protocol.Completion
	if err := env.Decode(&comp); err != nil {
		return nil, nil, err
	}
	var lease protocol.Lease
	if err := exec.Lease.Decode(&lease); err != nil {
		return nil, nil, err
	}
	switch {
	case comp.ExecID != exec.ExecID || comp.CalcID != lease.CalcID || comp.LeaseID != protocol.LeaseID(exec.Lease):
		return nil, nil, errors.New("completion does not belong to this lease")
	case comp.RequesterPeerID != a.id.PeerID.String() || comp.WorkerPeerID != worker.String():
		return nil, nil, errors.New("completion names the wrong peers")
	case comp.InputBundleDigest != lease.InputBundleDigest || comp.ArtifactDigest != lease.ArtifactDigest || comp.SolverManifestDigest != lease.SolverManifestDigest:
		return nil, nil, errors.New("completion does not match the leased input or solver")
	}
	n := len(st.SealedChunkHashes)
	if st.SealedChunkSize != uint32(seal.SealedChunkSize(bundle.ChunkSize)) || n != bundle.NumChunks(st.SealedSize, int(st.SealedChunkSize)) {
		return nil, nil, errors.New("sealed bundle layout is invalid")
	}
	leaves := make([][32]byte, n)
	for i, h := range st.SealedChunkHashes {
		if len(h) != 32 {
			return nil, nil, errors.New("invalid sealed chunk hash")
		}
		copy(leaves[i][:], h)
	}
	if bundle.Root(leaves, st.SealedSize) != comp.SealedBundleDigest {
		return nil, nil, errors.New("sealed chunk hashes do not match the signed completion")
	}
	return &comp, env, nil
}

func (a *Agent) downloadSealed(c *protocol.Conn, setDeadline func(time.Time) error, execID string, st *pb.Status) error {
	dir := a.paths.ExecDir(execID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	leaves := make([][32]byte, len(st.SealedChunkHashes))
	for i, h := range st.SealedChunkHashes {
		copy(leaves[i][:], h)
	}
	rx, err := openReceiver(filepath.Join(dir, "sealed.part"), filepath.Join(dir, "sealed.have"), st.SealedSize, int(st.SealedChunkSize), leaves)
	if err != nil {
		return err
	}
	defer rx.Close()
	missing := rx.Missing()
	for len(missing) > 0 {
		batch := missing[:min(len(missing), 32)]
		missing = missing[len(batch):]
		setDeadline(time.Now().Add(5 * time.Minute))
		if err := c.Send(&pb.Frame{Body: &pb.Frame_ChunkRequest{ChunkRequest: &pb.ChunkRequest{ExecId: execID, Indices: batch}}}); err != nil {
			return err
		}
		for range batch {
			f, err := c.Recv()
			if err != nil {
				return err
			}
			ch := f.GetChunk()
			if ch == nil {
				return errors.New("expected chunk")
			}
			if err := rx.Put(ch.Index, ch.Data); err != nil {
				return err
			}
		}
	}
	return nil
}

// finalize decrypts, verifies, unpacks, parses and signs the acceptance.
func (a *Agent) finalize(ctx context.Context, execID string, comp *protocol.Completion, compEnv *envelope.Envelope, enc []byte) error {
	exec, err := a.store.Get(execID)
	if err != nil {
		return err
	}
	dir := a.paths.ExecDir(execID)
	rk, err := a.loadRecipientKey(execID)
	if err != nil {
		return fmt.Errorf("recipient key: %w", err)
	}
	op, err := seal.NewOpener(rk, enc, execID)
	if err != nil {
		return err
	}
	sealed, err := os.ReadFile(filepath.Join(dir, "sealed.part"))
	if err != nil {
		return err
	}
	var plain bytes.Buffer
	step := seal.SealedChunkSize(bundle.ChunkSize)
	for i := 0; i*step < len(sealed); i++ {
		p, err := op.OpenChunk(uint64(i), sealed[i*step:min((i+1)*step, len(sealed))])
		if err != nil {
			return err
		}
		plain.Write(p)
	}

	verdict := protocol.VerdictAccepted
	parsedDigest := ""
	var lease protocol.Lease
	exec.Lease.Decode(&lease)
	sv, err := a.manifestFor(lease.SolverManifestDigest)
	if err != nil {
		return err
	}
	parserDigest := sv.Manifest.Parser.Digest

	if bundle.Digest(plain.Bytes(), bundle.ChunkSize) != comp.OutputBundleDigest {
		verdict = protocol.VerdictOutputMismatch
	} else {
		outDir := exec.ResultDir
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
		os.RemoveAll(filepath.Join(outDir, "output"))
		if _, err := bundle.Extract(bytes.NewReader(plain.Bytes()), outDir, bundle.Limits{MaxFiles: 1000, MaxTotalBytes: lease.MaxOutputBytes + (1 << 20)}); err != nil {
			verdict = protocol.VerdictMalformedOutput
		} else {
			wasm, _ := parser.Builtin(parserDigest)
			parsed, err := parser.RunCached(ctx, wasm, filepath.Join(outDir, "output"), filepath.Join(a.paths.Home, "cache", "wazero"))
			if err != nil {
				return fmt.Errorf("parse: %w", err)
			}
			parsedDigest = contentid.BLAKE3(parsed)
			if err := writeFile(filepath.Join(outDir, "parsed", "result.json"), parsed); err != nil {
				return err
			}
			prov := filepath.Join(outDir, "provenance")
			os.MkdirAll(prov, 0o755)
			os.Rename(filepath.Join(outDir, "execution-environment.json"), filepath.Join(prov, "execution-environment.json"))
			for name, env := range map[string]*envelope.Envelope{"lease.json": exec.Lease, "receipt-completion.json": compEnv, "solver-manifest.json": sv.Envelope} {
				raw, _ := env.MarshalFile()
				if err := writeFile(filepath.Join(prov, name), raw); err != nil {
					return err
				}
			}
		}
	}
	if err := a.store.Transition(execID, []string{store.StateSealed}, store.StateDelivered, store.Update{}, map[string]any{"verdict": verdict}); err != nil {
		return err
	}

	acc := protocol.Acceptance{
		Schema: protocol.SchemaAcceptance, ReceiptID: protocol.ReceiptID(compEnv), ExecID: execID,
		OutputBundleDigest: comp.OutputBundleDigest, ParserDigest: parserDigest,
		ParsedResultDigest: parsedDigest, AcceptedAt: protocol.Now(time.Now()), Verdict: verdict,
	}
	accEnv, err := envelope.Sign(a.id, protocol.SchemaAcceptance, acc)
	if err != nil {
		return err
	}
	if verdict == protocol.VerdictAccepted {
		raw, _ := accEnv.MarshalFile()
		writeFile(filepath.Join(exec.ResultDir, "provenance", "receipt-acceptance.json"), raw)
	}
	if err := a.store.Transition(execID, []string{store.StateDelivered}, store.StateAcknowledged, store.Update{Acceptance: accEnv}, nil); err != nil {
		return err
	}
	// The private key and ciphertext are no longer needed once the result is
	// decrypted and acknowledged (§15.3). Until then a crash can redo finalize.
	os.Remove(a.recipientKeyPath(execID))
	os.RemoveAll(dir)
	return nil
}

func (a *Agent) sendAck(c *protocol.Conn, exec *store.Execution) (string, error) {
	if exec.Acceptance == nil {
		return exec.State, errors.New("no acceptance to send")
	}
	if err := c.Send(&pb.Frame{Body: &pb.Frame_Ack{Ack: &pb.Ack{Acceptance: protocol.ToPB(exec.Acceptance)}}}); err != nil {
		return exec.State, err
	}
	f, err := c.Recv()
	if err != nil {
		return exec.State, err
	}
	if f.GetAckResult() == nil || !f.GetAckResult().Ok {
		return exec.State, errors.New("worker rejected the acknowledgment")
	}
	if err := a.store.Transition(exec.ExecID, []string{store.StateAcknowledged}, store.StateCompleted, store.Update{}, nil); err != nil {
		return "", err
	}
	a.log.Info("receipt completed", "exec", exec.ExecID, "result", exec.ResultDir)
	return store.StateCompleted, nil
}

// --- helpers ----------------------------------------------------------------

func (a *Agent) recipientKeyPath(execID string) string {
	return filepath.Join(a.paths.Keys(), execDirName(execID)+".x25519")
}

func (a *Agent) saveRecipientKey(execID string, k *seal.RecipientKey) error {
	if err := os.MkdirAll(a.paths.Keys(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(a.recipientKeyPath(execID), k.Bytes(), 0o600)
}

func (a *Agent) loadRecipientKey(execID string) (*seal.RecipientKey, error) {
	raw, err := os.ReadFile(a.recipientKeyPath(execID))
	if err != nil {
		return nil, err
	}
	return seal.ParseRecipientKey(raw)
}

func writeInputRecord(dir string, canonicalJob, jobYAML []byte, files map[string][]byte) error {
	in := filepath.Join(dir, "input")
	if err := writeFile(filepath.Join(in, "canonical-job.json"), canonicalJob); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(in, "job.yaml"), jobYAML); err != nil {
		return err
	}
	deps := map[string]string{}
	for name, d := range files {
		if err := writeFile(filepath.Join(in, name), d); err != nil {
			return err
		}
		deps[name] = contentid.SHA256(d)
	}
	raw, _ := json.MarshalIndent(deps, "", "  ")
	return writeFile(filepath.Join(in, "dependencies.json"), append(raw, '\n'))
}

func writeFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func shortPeer(p peer.ID) string {
	s := p.String()
	if len(s) <= 10 {
		return s
	}
	return s[:6] + "..." + s[len(s)-4:]
}
