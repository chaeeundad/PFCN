package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/pb"
	"github.com/chaeeundad/PFCN/internal/protocol"
	"github.com/chaeeundad/PFCN/internal/store"
)

// activeWorkerJobs counts leased and running executions plus pending offers.
func (a *Agent) activeWorkerJobs() (int, error) {
	list, err := a.store.List(store.RoleWorker, true)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range list {
		switch e.State {
		case store.StateLeased, store.StateInputTransfer, store.StateRunning:
			n++
		}
	}
	a.mu.Lock()
	n += a.reserved
	a.mu.Unlock()
	return n, nil
}

func (a *Agent) status() (string, error) {
	mode := a.Mode()
	if mode != ModeAvailable {
		return mode, nil
	}
	n, err := a.activeWorkerJobs()
	if err != nil {
		return "", err
	}
	if n >= a.policy.MaxConcurrent {
		return "busy", nil
	}
	return ModeAvailable, nil
}

// Capability builds a fresh signed capability document (§11.2).
func (a *Agent) Capability() (*envelope.Envelope, error) {
	st, err := a.status()
	if err != nil {
		return nil, err
	}
	seq, err := a.store.NextSequence("capability_sequence")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	cores := a.policy.CPUCores
	if st != ModeAvailable {
		cores = 0
	}
	var solvers []protocol.CapabilitySolver
	for _, name := range a.cfg.Solvers.Allow {
		solvers = append(solvers, protocol.CapabilitySolver{Name: name})
	}
	if solvers == nil {
		solvers = []protocol.CapabilitySolver{}
	}
	c := protocol.Capability{
		Schema: protocol.SchemaCapability, PeerID: a.id.PeerID.String(), Namespace: a.cfg.Namespace,
		Sequence: seq, IssuedAt: protocol.Now(now), ExpiresAt: protocol.Now(now.Add(protocol.CapabilityTTL)),
		Status: st, Platform: a.platform,
		CPU:         protocol.CapabilityCPU{Model: a.hw.CPUModel, AvailableCores: cores},
		MemoryBytes: a.policy.MemoryBytes,
		Runtime: protocol.CapabilityRuntime{
			Engine: a.runtimeName(), Container: a.rt != nil,
			// Workspace encryption is a Phase 3 deliverable; advertise honestly.
			WorkspaceEncryption: false,
		},
		Solvers: solvers,
		Policy: protocol.CapabilityPolicy{
			MaxWalltimeSeconds:        a.policy.MaxWalltimeSeconds,
			MaxResultRetentionSeconds: a.policy.MaxResultRetentionSeconds,
			MaxInputBytes:             a.policy.MaxInputBytes,
			Network:                   "deny",
			Visibility:                a.policy.AcceptVisibility,
		},
	}
	return envelope.Sign(a.id, protocol.SchemaCapability, c)
}

func (a *Agent) handleCapability(s network.Stream) {
	defer s.Close()
	s.SetDeadline(time.Now().Add(30 * time.Second))
	c := protocol.NewConn(s)
	f, err := c.Recv()
	if err != nil || f.GetCapabilityRequest() == nil {
		return
	}
	if ns := f.GetCapabilityRequest().Namespace; ns != a.cfg.Namespace {
		c.SendError(protocol.ErrCodeRejected, fmt.Errorf("namespace %q not served", ns))
		return
	}
	env, err := a.Capability()
	if err != nil {
		c.SendError(protocol.ErrCodeInternal, err)
		return
	}
	c.Send(&pb.Frame{Body: &pb.Frame_Capability{Capability: protocol.ToPB(env)}})
}

// QueryCapability fetches and verifies a peer's capability document.
func (a *Agent) QueryCapability(ctx context.Context, p peer.ID) (*protocol.Capability, error) {
	s, err := a.host.NewStream(ctx, p, protocol.ProtoCapability)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	s.SetDeadline(time.Now().Add(30 * time.Second))
	c := protocol.NewConn(s)
	if err := c.Send(&pb.Frame{Body: &pb.Frame_CapabilityRequest{CapabilityRequest: &pb.CapabilityRequest{Namespace: a.cfg.Namespace}}}); err != nil {
		return nil, err
	}
	f, err := c.Recv()
	if err != nil {
		return nil, err
	}
	env, err := protocol.FromPB(f.GetCapability())
	if err != nil {
		return nil, err
	}
	if err := env.VerifySigners(protocol.SchemaCapability, p.String()); err != nil {
		return nil, err
	}
	var cap protocol.Capability
	if err := env.Decode(&cap); err != nil {
		return nil, err
	}
	now := time.Now()
	if err := protocol.CheckIssued(cap.IssuedAt, now); err != nil {
		return nil, err
	}
	exp, err := protocol.ParseTime(cap.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if now.After(exp.Add(protocol.ClockSkew)) {
		return nil, fmt.Errorf("capability from %s is expired", p)
	}
	if cap.PeerID != p.String() || cap.Namespace != a.cfg.Namespace {
		return nil, fmt.Errorf("capability identity/namespace mismatch")
	}
	return &cap, nil
}
