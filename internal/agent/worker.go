package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/libp2p/go-libp2p/core/network"

	"github.com/chaeeundad/PFCN/internal/bundle"
	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/pb"
	"github.com/chaeeundad/PFCN/internal/protocol"
	"github.com/chaeeundad/PFCN/internal/seal"
	"github.com/chaeeundad/PFCN/internal/solver"
	"github.com/chaeeundad/PFCN/internal/store"
)

var nonceRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// minInputWindow is the minimum time a requester gets to upload input.
const minInputWindow = 10 * time.Minute

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// handleLease runs the worker side of /pumat/lease/1.0.0 (§14.4, §15).
func (a *Agent) handleLease(s network.Stream) {
	defer s.Close()
	s.SetDeadline(time.Now().Add(60 * time.Second))
	c := protocol.NewConn(s)
	remote := s.Conn().RemotePeer().String()

	f, err := c.Recv()
	if err != nil || f.GetLeaseRequest() == nil {
		return
	}
	offer, verified, err := a.evaluateLeaseRequest(remote, f.GetLeaseRequest())
	if err != nil {
		a.log.Info("lease rejected", "from", remote, "err", err)
		c.SendError(protocol.ErrCodeRejected, err)
		return
	}

	a.mu.Lock()
	a.reserved++
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.reserved--
		a.mu.Unlock()
	}()

	if err := c.Send(&pb.Frame{Body: &pb.Frame_LeaseOffer{LeaseOffer: protocol.ToPB(offer)}}); err != nil {
		return
	}
	f, err = c.Recv()
	if err != nil || f.GetLeaseCountersigned() == nil {
		return
	}
	signed, err := protocol.FromPB(f.GetLeaseCountersigned())
	if err != nil {
		c.SendError(protocol.ErrCodeInvalid, err)
		return
	}
	var lease protocol.Lease
	if !signed.SamePayload(offer) {
		c.SendError(protocol.ErrCodeInvalid, errors.New("countersigned lease differs from offer"))
		return
	}
	if err := signed.VerifySigners(protocol.SchemaLease, a.id.PeerID.String(), remote); err != nil {
		c.SendError(protocol.ErrCodeInvalid, err)
		return
	}
	if err := signed.Decode(&lease); err != nil {
		c.SendError(protocol.ErrCodeInvalid, err)
		return
	}
	deadline, _ := protocol.ParseTime(lease.InputDeadline)
	exec := &store.Execution{
		ExecID: lease.ExecID, Role: store.RoleWorker, State: store.StateLeased,
		CalcID: lease.CalcID, PeerID: remote, Lease: signed, InputDeadline: deadline,
	}
	if err := a.store.Insert(exec, "lease", map[string]any{"lease_id": protocol.LeaseID(signed)}); err != nil {
		// A duplicate exec_id is a replayed or repeated lease.
		c.SendError(protocol.ErrCodeRejected, errors.New("execution already exists"))
		return
	}
	if err := os.MkdirAll(a.paths.ExecDir(lease.ExecID), 0o700); err != nil {
		c.SendError(protocol.ErrCodeInternal, err)
		return
	}
	c.Send(&pb.Frame{Body: &pb.Frame_LeaseConfirm{LeaseConfirm: &pb.LeaseConfirm{LeaseId: protocol.LeaseID(signed)}}})
	a.log.Info("lease accepted", "exec", lease.ExecID, "requester", remote)

	// Warm the solver image while input uploads.
	image := imageRef(verified, lease.Platform)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := a.rt.EnsureImage(ctx, image); err != nil {
			a.log.Warn("solver image pull failed", "image", image, "err", err)
		}
	}()
}

func imageRef(v *solver.Verified, platform string) string {
	art, _ := v.Manifest.ArtifactFor(platform)
	return art.Reference + "@" + art.Digest
}

// evaluateLeaseRequest applies local policy and returns a worker-signed lease.
func (a *Agent) evaluateLeaseRequest(remote string, lr *pb.LeaseRequest) (*envelope.Envelope, *solver.Verified, error) {
	if a.rt == nil {
		return nil, nil, errors.New("worker has no container runtime")
	}
	st, err := a.status()
	if err != nil {
		return nil, nil, err
	}
	if st != ModeAvailable {
		return nil, nil, fmt.Errorf("worker is %s", st)
	}
	reqEnv, err := protocol.FromPB(lr.Request)
	if err != nil {
		return nil, nil, err
	}
	if err := reqEnv.VerifySigners(protocol.SchemaLeaseRequest, remote); err != nil {
		return nil, nil, err
	}
	var r protocol.LeaseRequest
	if err := reqEnv.Decode(&r); err != nil {
		return nil, nil, err
	}
	now := time.Now()
	switch {
	case r.RequesterPeerID != remote:
		return nil, nil, errors.New("requester_peer_id does not match the connection")
	case r.WorkerPeerID != a.id.PeerID.String():
		return nil, nil, errors.New("lease request is addressed to a different worker")
	case r.Namespace != a.cfg.Namespace:
		return nil, nil, fmt.Errorf("namespace %q not served", r.Namespace)
	case !nonceRe.MatchString(r.RequesterNonce):
		return nil, nil, errors.New("requester_nonce must be 128-bit hex")
	case r.CustodianPeerID != "":
		return nil, nil, errors.New("custodian delivery is not supported yet")
	}
	if err := protocol.CheckIssued(r.IssuedAt, now); err != nil {
		return nil, nil, err
	}
	if issued, _ := protocol.ParseTime(r.IssuedAt); now.Sub(issued) > protocol.ClockSkew+time.Minute {
		return nil, nil, errors.New("lease request is stale")
	}
	wantExec, err := protocol.ExecID(r.CalcID, r.RequesterPeerID, r.WorkerPeerID, r.RequesterNonce)
	if err != nil || wantExec != r.ExecID {
		return nil, nil, errors.New("exec_id does not match its derivation")
	}
	if _, err := a.store.Get(r.ExecID); err == nil {
		return nil, nil, errors.New("execution already exists (replay)")
	}

	// Policy limits (§27).
	pol := a.policy
	switch {
	case r.Resources.CPUMillicores <= 0 || r.Resources.CPUMillicores%1000 != 0 || r.Resources.CPUMillicores > pol.CPUCores*1000:
		return nil, nil, fmt.Errorf("requested CPU exceeds the %d cores this node offers", pol.CPUCores)
	case r.Resources.MemoryBytes <= 0 || r.Resources.MemoryBytes > pol.MemoryBytes:
		return nil, nil, errors.New("requested memory exceeds the node's offer")
	case r.Resources.GPUCount != 0:
		return nil, nil, errors.New("GPU jobs are not supported")
	case r.MaxWalltimeSeconds <= 0 || r.MaxWalltimeSeconds > pol.MaxWalltimeSeconds:
		return nil, nil, errors.New("requested walltime exceeds the node's limit")
	case r.InputBundleSize <= 0 || r.InputBundleSize > pol.MaxInputBytes:
		return nil, nil, errors.New("input bundle size exceeds the node's limit")
	case !slices.Contains(pol.AcceptVisibility, r.Visibility):
		return nil, nil, fmt.Errorf("node does not accept %s jobs", r.Visibility)
	case r.ResultRetentionSeconds < 300:
		return nil, nil, errors.New("result retention must be at least 5 minutes")
	}
	if pol.TrustedAfter > 0 && r.MaxWalltimeSeconds > pol.NewRequesterMaxWalltime {
		stats, err := a.store.Stats(store.RoleWorker)
		if err != nil {
			return nil, nil, err
		}
		if st := stats[store.RoleWorker+"|"+remote]; st == nil || st.Completed < pol.TrustedAfter {
			return nil, nil, fmt.Errorf("new requesters are limited to %ds walltime on this node until %d jobs complete here", pol.NewRequesterMaxWalltime, pol.TrustedAfter)
		}
	}
	if _, err := seal.ParsePublic(r.ResultRecipientKey); err != nil {
		return nil, nil, err
	}
	if _, _, err := parseBundleDigest(r.InputBundleDigest); err != nil {
		return nil, nil, err
	}

	// Solver trust (§13.2): signature, digest pin, entrypoint, platform.
	manEnv, err := protocol.FromPB(lr.SolverManifest)
	if err != nil {
		return nil, nil, err
	}
	v, err := a.Trust().Verify(manEnv)
	if err != nil {
		return nil, nil, err
	}
	if err := a.checkRevocation(v); err != nil {
		return nil, nil, err
	}
	if v.Digest != r.SolverManifestDigest {
		return nil, nil, errors.New("solver manifest digest does not match the request")
	}
	ep, ok := v.Manifest.Entrypoints[r.Entrypoint]
	if !ok {
		return nil, nil, fmt.Errorf("entrypoint %q is not declared by the solver manifest", r.Entrypoint)
	}
	if r.MaxWalltimeSeconds > ep.MaxWalltimeSeconds {
		return nil, nil, errors.New("walltime exceeds the entrypoint limit")
	}
	art, ok := v.Manifest.ArtifactFor(a.platform)
	if !ok {
		return nil, nil, fmt.Errorf("solver has no artifact for %s", a.platform)
	}

	retention := min(r.ResultRetentionSeconds, pol.MaxResultRetentionSeconds)
	window := max(minInputWindow, time.Duration(r.InputBundleSize/(1<<20))*time.Second*2)
	lease := protocol.Lease{
		Schema: protocol.SchemaLease, Namespace: r.Namespace, ExecID: r.ExecID, CalcID: r.CalcID,
		RequesterPeerID: r.RequesterPeerID, WorkerPeerID: r.WorkerPeerID,
		RequesterNonce: r.RequesterNonce, WorkerNonce: randomHex(16),
		SolverManifestDigest: r.SolverManifestDigest, ArtifactDigest: art.Digest, Platform: a.platform,
		Entrypoint: r.Entrypoint, Visibility: r.Visibility,
		Resources:         protocol.Resources{CPUMillicores: r.Resources.CPUMillicores, MemoryBytes: r.Resources.MemoryBytes},
		InputBundleDigest: r.InputBundleDigest, InputBundleSize: r.InputBundleSize,
		MaxOutputBytes: pol.MaxOutputBytes,
		IssuedAt:       protocol.Now(now), InputDeadline: protocol.Now(now.Add(window)),
		MaxWalltimeSeconds: r.MaxWalltimeSeconds, ResultRetentionSeconds: retention,
		ResultRecipientKey: r.ResultRecipientKey,
	}
	env, err := envelope.Sign(a.id, protocol.SchemaLease, lease)
	return env, v, err
}

func parseBundleDigest(d string) (string, []byte, error) {
	if len(d) < 8 || d[:7] != "blake3:" {
		return "", nil, errors.New("bundle digests must be blake3")
	}
	b, err := hex.DecodeString(d[7:])
	if err != nil || len(b) != 32 {
		return "", nil, errors.New("invalid bundle digest")
	}
	return "blake3", b, nil
}

// handleInput receives the input bundle (§16.3, §16.4).
func (a *Agent) handleInput(s network.Stream) {
	defer s.Close()
	c := protocol.NewConn(s)
	remote := s.Conn().RemotePeer().String()
	s.SetDeadline(time.Now().Add(60 * time.Second))
	f, err := c.Recv()
	if err != nil || f.GetInputBegin() == nil {
		return
	}
	ib := f.GetInputBegin()
	exec, lease, err := a.workerExec(ib.ExecId, remote)
	if err != nil {
		c.SendError(protocol.ErrCodeUnauthorized, err)
		return
	}
	if exec.State != store.StateLeased && exec.State != store.StateInputTransfer {
		c.SendError(protocol.ErrCodeRejected, fmt.Errorf("execution is %s", exec.State))
		return
	}
	if time.Now().After(exec.InputDeadline.Add(protocol.ClockSkew)) {
		c.SendError(protocol.ErrCodeRejected, errors.New("input deadline passed"))
		return
	}
	n := bundle.NumChunks(ib.Size, bundle.ChunkSize)
	if ib.Size != lease.InputBundleSize || ib.ChunkSize != bundle.ChunkSize || len(ib.ChunkHashes) != n {
		c.SendError(protocol.ErrCodeInvalid, errors.New("input header does not match the lease"))
		return
	}
	leaves := make([][32]byte, n)
	for i, h := range ib.ChunkHashes {
		if len(h) != 32 {
			c.SendError(protocol.ErrCodeInvalid, errors.New("invalid chunk hash"))
			return
		}
		copy(leaves[i][:], h)
	}
	if bundle.Root(leaves, ib.Size) != lease.InputBundleDigest {
		c.SendError(protocol.ErrCodeInvalid, errors.New("chunk hashes do not match the leased input digest"))
		return
	}
	if exec.State == store.StateLeased {
		if err := a.store.Transition(exec.ExecID, []string{store.StateLeased}, store.StateInputTransfer, store.Update{}, nil); err != nil {
			c.SendError(protocol.ErrCodeInternal, err)
			return
		}
	}

	dir := a.paths.ExecDir(exec.ExecID)
	rx, err := openReceiver(filepath.Join(dir, "input.bundle"), filepath.Join(dir, "input.have"), ib.Size, bundle.ChunkSize, leaves)
	if err != nil {
		c.SendError(protocol.ErrCodeInternal, err)
		return
	}
	defer rx.Close()
	if err := c.Send(&pb.Frame{Body: &pb.Frame_InputHave{InputHave: &pb.InputHave{Missing: rx.Missing()}}}); err != nil {
		return
	}
	for !rx.Complete() {
		s.SetDeadline(time.Now().Add(2 * time.Minute))
		f, err := c.Recv()
		if err != nil {
			return // resumable: the requester reconnects and resends missing chunks
		}
		ch := f.GetChunk()
		if ch == nil {
			c.SendError(protocol.ErrCodeInvalid, errors.New("expected chunk"))
			return
		}
		if err := rx.Put(ch.Index, ch.Data); err != nil {
			c.SendError(protocol.ErrCodeInvalid, err)
			return
		}
	}
	if _, _, d, err := bundle.FileDigest(filepath.Join(dir, "input.bundle"), bundle.ChunkSize); err != nil || d != lease.InputBundleDigest {
		c.SendError(protocol.ErrCodeInvalid, errors.New("assembled input does not match the leased digest"))
		return
	}
	if err := a.store.Transition(exec.ExecID, []string{store.StateInputTransfer}, store.StateRunning, store.Update{}, nil); err != nil {
		c.SendError(protocol.ErrCodeInternal, err)
		return
	}
	c.Send(&pb.Frame{Body: &pb.Frame_InputDone{InputDone: &pb.InputDone{Ok: true}}})
	a.startExecution(exec.ExecID)
}

// workerExec loads a worker execution and checks the caller is its requester.
func (a *Agent) workerExec(execID, remote string) (*store.Execution, *protocol.Lease, error) {
	exec, err := a.store.Get(execID)
	if err != nil || exec.Role != store.RoleWorker {
		return nil, nil, errors.New("unknown execution")
	}
	if exec.PeerID != remote {
		return nil, nil, errors.New("only the requester of this lease may access it")
	}
	var lease protocol.Lease
	if err := exec.Lease.Decode(&lease); err != nil {
		return nil, nil, err
	}
	return exec, &lease, nil
}

// handleResult serves status, sealed chunks and acknowledgments (§16.8).
func (a *Agent) handleResult(s network.Stream) {
	defer s.Close()
	c := protocol.NewConn(s)
	remote := s.Conn().RemotePeer().String()
	for {
		s.SetDeadline(time.Now().Add(2 * time.Minute))
		f, err := c.Recv()
		if err != nil {
			return
		}
		switch {
		case f.GetStatusRequest() != nil:
			st, err := a.resultStatus(f.GetStatusRequest().ExecId, remote)
			if err != nil {
				c.SendError(protocol.ErrCodeNotFound, err)
				return
			}
			if err := c.Send(&pb.Frame{Body: &pb.Frame_Status{Status: st}}); err != nil {
				return
			}
		case f.GetChunkRequest() != nil:
			if err := a.serveChunks(c, f.GetChunkRequest(), remote); err != nil {
				c.SendError(protocol.ErrCodeInvalid, err)
				return
			}
		case f.GetCancel() != nil:
			if err := a.acceptCancel(f.GetCancel(), remote); err != nil {
				c.SendError(protocol.ErrCodeRejected, err)
				return
			}
			if err := c.Send(&pb.Frame{Body: &pb.Frame_AckResult{AckResult: &pb.AckResult{Ok: true}}}); err != nil {
				return
			}
		case f.GetAck() != nil:
			if err := a.acceptAck(f.GetAck(), remote); err != nil {
				c.SendError(protocol.ErrCodeInvalid, err)
				return
			}
			if err := c.Send(&pb.Frame{Body: &pb.Frame_AckResult{AckResult: &pb.AckResult{Ok: true}}}); err != nil {
				return
			}
		default:
			c.SendError(protocol.ErrCodeInvalid, errors.New("unexpected frame"))
			return
		}
	}
}

func (a *Agent) resultStatus(execID, remote string) (*pb.Status, error) {
	exec, _, err := a.workerExec(execID, remote)
	if err != nil {
		return nil, err
	}
	st := &pb.Status{ExecId: execID, State: exec.State, Message: exec.Error, RetentionDeadlineUnix: unixOrZero(exec.RetentionDeadline)}
	if exec.State == store.StateRunning {
		st.ElapsedSeconds = int64(time.Since(exec.UpdatedAt).Seconds())
	}
	if exec.Completion != nil {
		st.Completion = protocol.ToPB(exec.Completion)
	}
	if exec.State == store.StateSealed {
		sd := filepath.Join(a.paths.ExecDir(execID), "sealed")
		enc, err := os.ReadFile(filepath.Join(sd, "enc"))
		if err != nil {
			return nil, err
		}
		leaves, size, _, err := bundle.FileDigest(filepath.Join(sd, "bundle.sealed"), seal.SealedChunkSize(bundle.ChunkSize))
		if err != nil {
			return nil, err
		}
		st.Enc, st.SealedSize, st.SealedChunkSize = enc, size, uint32(seal.SealedChunkSize(bundle.ChunkSize))
		for _, l := range leaves {
			st.SealedChunkHashes = append(st.SealedChunkHashes, append([]byte(nil), l[:]...))
		}
	}
	return st, nil
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func (a *Agent) serveChunks(c *protocol.Conn, req *pb.ChunkRequest, remote string) error {
	exec, _, err := a.workerExec(req.ExecId, remote)
	if err != nil {
		return err
	}
	if exec.State != store.StateSealed {
		return fmt.Errorf("execution is %s", exec.State)
	}
	if len(req.Indices) > 64 {
		return errors.New("at most 64 chunks per request")
	}
	f, err := os.Open(filepath.Join(a.paths.ExecDir(req.ExecId), "sealed", "bundle.sealed"))
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	cs := int64(seal.SealedChunkSize(bundle.ChunkSize))
	n := bundle.NumChunks(st.Size(), int(cs))
	for _, idx := range req.Indices {
		if int(idx) >= n {
			return errors.New("chunk index out of range")
		}
		off := int64(idx) * cs
		size := min(cs, st.Size()-off)
		buf := make([]byte, size)
		if _, err := f.ReadAt(buf, off); err != nil {
			return err
		}
		if err := c.Send(&pb.Frame{Body: &pb.Frame_Chunk{Chunk: &pb.Chunk{Index: idx, Data: buf}}}); err != nil {
			return err
		}
	}
	return nil
}

// acceptAck verifies the requester acceptance and completes the execution.
func (a *Agent) acceptAck(ack *pb.Ack, remote string) error {
	env, err := protocol.FromPB(ack.Acceptance)
	if err != nil {
		return err
	}
	if err := env.VerifySigners(protocol.SchemaAcceptance, remote); err != nil {
		return err
	}
	var acc protocol.Acceptance
	if err := env.Decode(&acc); err != nil {
		return err
	}
	exec, _, err := a.workerExec(acc.ExecID, remote)
	if err != nil {
		return err
	}
	if exec.State == store.StateCompleted {
		return nil // idempotent re-ack
	}
	if exec.State != store.StateSealed || exec.Completion == nil {
		return fmt.Errorf("execution is %s", exec.State)
	}
	var comp protocol.Completion
	if err := exec.Completion.Decode(&comp); err != nil {
		return err
	}
	if acc.ReceiptID != protocol.ReceiptID(exec.Completion) || acc.OutputBundleDigest != comp.OutputBundleDigest {
		return errors.New("acceptance does not reference this completion")
	}
	if err := a.store.Transition(exec.ExecID, []string{store.StateSealed}, store.StateCompleted,
		store.Update{Acceptance: env}, map[string]any{"verdict": acc.Verdict}); err != nil {
		return err
	}
	if err := os.RemoveAll(a.paths.ExecDir(exec.ExecID)); err != nil {
		a.log.Warn("cleanup failed", "exec", exec.ExecID, "err", err)
	}
	a.log.Info("execution completed", "exec", exec.ExecID, "verdict", acc.Verdict)
	return nil
}

// recoverWorker handles executions interrupted by an agent restart.
func (a *Agent) recoverWorker() error {
	list, err := a.store.List(store.RoleWorker, true)
	if err != nil {
		return err
	}
	for _, e := range list {
		if e.State != store.StateRunning {
			continue
		}
		msg := "agent restarted during execution"
		if err := a.store.Transition(e.ExecID, []string{store.StateRunning}, store.StateFailed, store.Update{Error: &msg}, nil); err != nil {
			return err
		}
		os.RemoveAll(a.paths.ExecDir(e.ExecID))
	}
	return nil
}

// sweepLoop expires stale leases and held results (§15.4, §28.3).
func (a *Agent) sweepLoop(ctx context.Context) {
	defer a.wg.Done()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		a.sweep()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *Agent) sweep() {
	list, err := a.store.List(store.RoleWorker, true)
	if err != nil {
		a.log.Warn("sweep", "err", err)
		return
	}
	now := time.Now()
	for _, e := range list {
		switch {
		case (e.State == store.StateLeased || e.State == store.StateInputTransfer) && now.After(e.InputDeadline.Add(protocol.ClockSkew)):
			msg := "input not received before the deadline"
			if a.store.Transition(e.ExecID, []string{e.State}, store.StateExpired, store.Update{Error: &msg}, nil) == nil {
				os.RemoveAll(a.paths.ExecDir(e.ExecID))
				a.log.Info("lease expired", "exec", e.ExecID)
			}
		case e.State == store.StateSealed && !e.RetentionDeadline.IsZero() && now.After(e.RetentionDeadline):
			if a.store.Transition(e.ExecID, []string{store.StateSealed}, store.StateResultExpired, store.Update{}, nil) == nil {
				os.RemoveAll(a.paths.ExecDir(e.ExecID))
				a.log.Info("held result expired", "exec", e.ExecID)
			}
		}
	}
}

// acceptCancel stops an execution on the requester's signed request.
func (a *Agent) acceptCancel(p *pb.SignedEnvelope, remote string) error {
	env, err := protocol.FromPB(p)
	if err != nil {
		return err
	}
	if err := env.VerifySigners(protocol.SchemaCancel, remote); err != nil {
		return err
	}
	var c protocol.Cancel
	if err := env.Decode(&c); err != nil {
		return err
	}
	if err := protocol.CheckIssued(c.IssuedAt, time.Now()); err != nil {
		return err
	}
	exec, _, err := a.workerExec(c.ExecID, remote)
	if err != nil {
		return err
	}
	switch exec.State {
	case store.StateCancelled:
		return nil
	case store.StateLeased, store.StateInputTransfer, store.StateRunning:
	default:
		return fmt.Errorf("execution is %s and can no longer be cancelled", exec.State)
	}
	msg := "cancelled by requester"
	if err := a.store.Transition(c.ExecID, []string{exec.State}, store.StateCancelled, store.Update{Error: &msg}, nil); err != nil {
		return err
	}
	a.mu.Lock()
	stop := a.cancels[c.ExecID]
	a.mu.Unlock()
	if stop != nil {
		stop() // kills the container; startExecution removes the workspace
	} else {
		os.RemoveAll(a.paths.ExecDir(c.ExecID))
	}
	a.log.Info("execution cancelled", "exec", c.ExecID)
	return nil
}
