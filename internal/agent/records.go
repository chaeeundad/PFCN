package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ipfs/go-cid"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multihash"

	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/job"
	"github.com/chaeeundad/PFCN/internal/pb"
	"github.com/chaeeundad/PFCN/internal/protocol"
	"github.com/chaeeundad/PFCN/internal/record"
	"github.com/chaeeundad/PFCN/internal/store"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

// Public records (§22, §24, §45). Records live in <home>/records/<hex>/.
// Every node holding a record serves it over /pumat/record/1.0.0 and
// announces a DHT provider record, so the publisher, mirrors and indexers
// are interchangeable sources.

const (
	ProtoRecord   = "/pumat/record/1.0.0"
	ProtoAnnounce = "/pumat/announce/1.0.0"
	maxRecordFile = 1 << 30
)

func (p Paths) Records() string { return filepath.Join(p.Home, "records") }

func (a *Agent) recordsTopic() string { return a.cfg.Namespace + "/records/v1" }

func recordKey(recordID string) cid.Cid {
	mh, _ := multihash.Sum([]byte("pumat/v1/record/"+recordID), multihash.SHA2_256, -1)
	return cid.NewCidV1(cid.Raw, mh)
}

// RecordsDir is the root of locally held records.
func (a *Agent) RecordsDir() string { return a.paths.Records() }

// RecordDir returns the local directory for a record.
func (a *Agent) RecordDir(recordID string) (string, error) {
	hex, err := record.Hex(recordID)
	if err != nil {
		return "", err
	}
	return filepath.Join(a.paths.Records(), hex), nil
}

// LocalRecord loads and verifies a locally stored record manifest.
func (a *Agent) LocalRecord(recordID string) (*envelope.Envelope, *record.Manifest, error) {
	dir, err := a.RecordDir(recordID)
	if err != nil {
		return nil, nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, nil, err
	}
	env, err := envelope.ParseFile(raw)
	if err != nil {
		return nil, nil, err
	}
	if record.ID(env) != recordID {
		return nil, nil, errors.New("stored manifest does not match its record ID")
	}
	m, err := record.VerifyManifest(env)
	return env, m, err
}

// startRecords sets up GossipSub announcements and record protocols.
func (a *Agent) startRecords(ctx context.Context) error {
	a.host.SetStreamHandler(ProtoRecord, a.handleRecord)
	a.host.SetStreamHandler(ProtoAnnounce, a.handleAnnounce)
	ps, err := pubsub.NewGossipSub(ctx, a.host)
	if err != nil {
		return fmt.Errorf("pubsub: %w", err)
	}
	topic := a.recordsTopic()
	if err := ps.RegisterTopicValidator(topic, func(_ context.Context, _ peer.ID, msg *pubsub.Message) bool {
		env, err := envelope.ParseFile(msg.Data)
		if err != nil {
			return false
		}
		ann, err := record.VerifyAnnouncement(env)
		return err == nil && ann.Visibility == job.VisibilityPublic
	}); err != nil {
		return err
	}
	t, err := ps.Join(topic)
	if err != nil {
		return err
	}
	a.topic = t
	sub, err := t.Subscribe()
	if err != nil {
		return err
	}
	go func() {
		for {
			msg, err := sub.Next(ctx)
			if err != nil {
				return
			}
			env, err := envelope.ParseFile(msg.Data)
			if err != nil {
				continue
			}
			if ann, err := record.VerifyAnnouncement(env); err == nil {
				a.deliverAnnouncement(ann, env, msg.ReceivedFrom)
			}
		}
	}()
	// Re-provide local records so mirrors stay discoverable.
	go func() {
		entries, _ := os.ReadDir(a.paths.Records())
		for _, e := range entries {
			id := contentid.Typed(contentid.KindRecord, "blake3:"+e.Name())
			if _, _, err := a.LocalRecord(id); err == nil {
				pctx, cancel := context.WithTimeout(ctx, time.Minute)
				a.disc.dht.Provide(pctx, recordKey(id), true)
				cancel()
			}
		}
	}()
	return nil
}

// OnAnnouncement registers a callback for verified announcements (indexers).
func (a *Agent) OnAnnouncement(fn func(*record.Announcement, *envelope.Envelope, peer.ID)) {
	a.mu.Lock()
	a.onAnnounce = fn
	a.mu.Unlock()
}

func (a *Agent) deliverAnnouncement(ann *record.Announcement, env *envelope.Envelope, from peer.ID) {
	a.mu.Lock()
	fn := a.onAnnounce
	a.mu.Unlock()
	if fn != nil {
		fn(ann, env, from)
	}
}

// PublishResult is returned by Publish.
type PublishResult struct {
	RecordID  string `json:"record_id"`
	Dir       string `json:"dir"`
	Announced int    `json:"announced_to_indexers"`
}

// Publish turns a completed requester execution into a public record (§45).
func (a *Agent) Publish(ctx context.Context, execID string) (*PublishResult, error) {
	exec, err := a.store.Find(execID)
	if err != nil {
		return nil, err
	}
	if exec.Role != store.RoleRequester || exec.State != store.StateCompleted {
		return nil, fmt.Errorf("only completed requester executions can be published (this one is %s %s)", exec.Role, exec.State)
	}
	var lease protocol.Lease
	if err := exec.Lease.Decode(&lease); err != nil {
		return nil, err
	}
	sv, err := a.manifestFor(lease.SolverManifestDigest)
	if err != nil {
		return nil, err
	}
	in := record.Inputs{
		ResultDir: exec.ResultDir, Namespace: a.cfg.Namespace, Lease: exec.Lease, Completion: exec.Completion,
		Acceptance: exec.Acceptance, SolverManifest: sv.Envelope, SolverName: sv.Manifest.Name, SolverVersion: sv.Manifest.Version,
	}
	if raw, err := os.ReadFile(filepath.Join(exec.ResultDir, "reproduction.json")); err == nil {
		var c record.Comparison
		if json.Unmarshal(raw, &c) == nil {
			in.Reproduces, in.Comparison = c.Against, &c
		}
	}
	env, err := record.Build(in, a.id, a.paths.Records())
	if err != nil {
		return nil, err
	}
	id := record.ID(env)
	dir, _ := a.RecordDir(id)
	a.store.AppendEvent("publish", map[string]any{"record_id": id, "exec_id": exec.ExecID})

	ann, err := record.Announce(env, a.id)
	if err != nil {
		return nil, err
	}
	res := &PublishResult{RecordID: id, Dir: dir}
	if a.disc != nil {
		pctx, cancel := context.WithTimeout(ctx, time.Minute)
		if err := a.disc.dht.Provide(pctx, recordKey(id), true); err != nil {
			a.log.Info("record provide deferred", "err", err)
		}
		cancel()
	}
	var m record.Manifest
	env.Decode(&m)
	if m.Visibility == job.VisibilityPublic {
		raw, _ := ann.MarshalFile()
		if a.topic != nil {
			if err := a.topic.Publish(ctx, raw); err != nil {
				a.log.Info("gossip publish failed", "err", err)
			}
		}
		res.Announced = a.pushAnnouncement(ctx, ann)
	}
	a.log.Info("record published", "record", id)
	return res, nil
}

// pushAnnouncement sends the announcement directly to configured indexers.
func (a *Agent) pushAnnouncement(ctx context.Context, ann *envelope.Envelope) int {
	infos, err := parseAddrInfos(a.cfg.Publication.Indexers)
	if err != nil {
		a.log.Warn("publication.indexers", "err", err)
		return 0
	}
	n := 0
	for _, info := range infos {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := func() error {
			if err := a.host.Connect(cctx, info); err != nil {
				return err
			}
			s, err := a.host.NewStream(cctx, info.ID, ProtoAnnounce)
			if err != nil {
				return err
			}
			defer s.Close()
			c := protocol.NewConn(s)
			if err := c.Send(&pb.Frame{Body: &pb.Frame_Announcement{Announcement: protocol.ToPB(ann)}}); err != nil {
				return err
			}
			f, err := c.Recv()
			if err != nil {
				return err
			}
			if f.GetAckResult() == nil || !f.GetAckResult().Ok {
				return errors.New("not acknowledged")
			}
			return nil
		}()
		cancel()
		if err != nil {
			a.log.Info("indexer push failed", "indexer", info.ID, "err", err)
			continue
		}
		n++
	}
	return n
}

func (a *Agent) handleAnnounce(s network.Stream) {
	defer s.Close()
	s.SetDeadline(time.Now().Add(30 * time.Second))
	c := protocol.NewConn(s)
	f, err := c.Recv()
	if err != nil {
		return
	}
	env, err := protocol.FromPB(f.GetAnnouncement())
	if err != nil {
		c.SendError(protocol.ErrCodeInvalid, err)
		return
	}
	ann, err := record.VerifyAnnouncement(env)
	if err != nil {
		c.SendError(protocol.ErrCodeInvalid, err)
		return
	}
	a.deliverAnnouncement(ann, env, s.Conn().RemotePeer())
	c.Send(&pb.Frame{Body: &pb.Frame_AckResult{AckResult: &pb.AckResult{Ok: true}}})
}

// handleRecord serves manifests and files of locally held records.
func (a *Agent) handleRecord(s network.Stream) {
	defer s.Close()
	c := protocol.NewConn(s)
	for {
		s.SetDeadline(time.Now().Add(2 * time.Minute))
		f, err := c.Recv()
		if err != nil {
			return
		}
		req := f.GetRecordRequest()
		if req == nil {
			c.SendError(protocol.ErrCodeInvalid, errors.New("expected record request"))
			return
		}
		env, m, err := a.LocalRecord(req.RecordId)
		if err != nil {
			c.SendError(protocol.ErrCodeNotFound, errors.New("record not held here"))
			return
		}
		if req.Path == "" {
			if err := c.Send(&pb.Frame{Body: &pb.Frame_RecordManifest{RecordManifest: protocol.ToPB(env)}}); err != nil {
				return
			}
			continue
		}
		p, err := record.CleanPath(req.Path)
		listed := false
		for _, fe := range m.Files {
			listed = listed || fe.Path == p
		}
		if err != nil || !listed {
			c.SendError(protocol.ErrCodeNotFound, errors.New("file not in record"))
			return
		}
		dir, _ := a.RecordDir(req.RecordId)
		if err := sendFile(c, filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			return
		}
	}
}

func sendFile(c *protocol.Conn, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	for i := uint32(0); ; i++ {
		n, err := io.ReadFull(f, buf)
		last := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
		if err != nil && !last {
			return err
		}
		if err := c.Send(&pb.Frame{Body: &pb.Frame_Chunk{Chunk: &pb.Chunk{Index: i, Data: buf[:n], Last: last}}}); err != nil {
			return err
		}
		if last {
			return nil
		}
	}
}

// FetchRecord downloads and verifies a record from hint peers or DHT
// providers and stores it locally, making this node a mirror (§24.4).
func (a *Agent) FetchRecord(ctx context.Context, recordID string, hints ...peer.ID) (*record.Manifest, error) {
	if _, m, err := a.LocalRecord(recordID); err == nil {
		return m, nil
	}
	if _, err := record.Hex(recordID); err != nil {
		return nil, err
	}
	cands := append([]peer.ID(nil), hints...)
	if a.disc != nil {
		fctx, cancel := context.WithTimeout(ctx, findTimeout)
		for info := range a.disc.dht.FindProvidersAsync(fctx, recordKey(recordID), 8) {
			if info.ID != a.id.PeerID {
				a.host.Peerstore().AddAddrs(info.ID, info.Addrs, 10*time.Minute)
				cands = append(cands, info.ID)
			}
		}
		cancel()
	}
	var lastErr error = errors.New("no provider found for this record")
	for _, p := range cands {
		m, err := a.fetchRecordFrom(ctx, p, recordID)
		if err == nil {
			return m, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (a *Agent) fetchRecordFrom(ctx context.Context, p peer.ID, recordID string) (*record.Manifest, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := a.host.Connect(cctx, peer.AddrInfo{ID: p}); err != nil {
		return nil, err
	}
	s, err := a.host.NewStream(cctx, p, ProtoRecord)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	c := protocol.NewConn(s)
	s.SetDeadline(time.Now().Add(time.Minute))
	if err := c.Send(&pb.Frame{Body: &pb.Frame_RecordRequest{RecordRequest: &pb.RecordRequest{RecordId: recordID}}}); err != nil {
		return nil, err
	}
	f, err := c.Recv()
	if err != nil {
		return nil, err
	}
	env, err := protocol.FromPB(f.GetRecordManifest())
	if err != nil {
		return nil, err
	}
	if record.ID(env) != recordID {
		return nil, errors.New("provider returned a manifest for a different record")
	}
	m, err := record.VerifyManifest(env)
	if err != nil {
		return nil, err
	}
	final, _ := a.RecordDir(recordID)
	tmp := final + ".partial"
	os.RemoveAll(tmp)
	for _, fe := range m.Files {
		s.SetDeadline(time.Now().Add(5 * time.Minute))
		if err := c.Send(&pb.Frame{Body: &pb.Frame_RecordRequest{RecordRequest: &pb.RecordRequest{RecordId: recordID, Path: fe.Path}}}); err != nil {
			return nil, err
		}
		dst := filepath.Join(tmp, filepath.FromSlash(fe.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		out, err := os.Create(dst)
		if err != nil {
			return nil, err
		}
		var got int64
		for {
			f, err := c.Recv()
			if err != nil {
				out.Close()
				return nil, err
			}
			ch := f.GetChunk()
			if ch == nil {
				out.Close()
				return nil, errors.New("expected chunk")
			}
			got += int64(len(ch.Data))
			if got > fe.Size || got > maxRecordFile {
				out.Close()
				return nil, fmt.Errorf("%s larger than declared", fe.Path)
			}
			out.Write(ch.Data)
			if ch.Last {
				break
			}
		}
		out.Close()
	}
	if err := record.VerifyFiles(m, tmp); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	raw, _ := env.MarshalFile()
	os.WriteFile(filepath.Join(tmp, "manifest.json"), raw, 0o644)
	os.MkdirAll(a.paths.Records(), 0o755)
	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	if a.disc != nil {
		go func() {
			pctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			a.disc.dht.Provide(pctx, recordKey(recordID), true)
		}()
	}
	return m, nil
}

// --- reproduction (§26) -------------------------------------------------------

// ReproduceRequest asks for an independent re-execution of a public record.
type ReproduceRequest struct {
	RecordID    string   `json:"record_id"`
	Peer        []string `json:"peer"`
	Detach      bool     `json:"detach"`
	EnergyTolEV float64  `json:"energy_tolerance_ev"`
	ForceTolEVA float64  `json:"force_tolerance_ev_per_angstrom"`
}

// Default tolerances for heterogeneous hardware (§26.2).
const (
	DefaultEnergyTolEV = 1e-4
	DefaultForceTolEVA = 1e-3
)

type reproTarget struct {
	RecordID    string  `json:"record_id"`
	EnergyTolEV float64 `json:"energy_tolerance_ev"`
	ForceTolEVA float64 `json:"force_tolerance_ev_per_angstrom"`
}

// Reproduce fetches a record, resubmits its canonical job to a different
// worker, and records a comparison once the result arrives.
func (a *Agent) Reproduce(ctx context.Context, req ReproduceRequest, progress func(string)) (*SubmitResult, error) {
	if progress == nil {
		progress = func(string) {}
	}
	progress("Fetching record " + req.RecordID + "...")
	m, err := a.FetchRecord(ctx, req.RecordID)
	if err != nil {
		return nil, err
	}
	dir, _ := a.RecordDir(req.RecordID)
	cjRaw, err := os.ReadFile(filepath.Join(dir, "input", "canonical-job.json"))
	if err != nil {
		return nil, errors.New("record does not publish its input; it cannot be reproduced (INSUFFICIENT_METADATA)")
	}
	cj, err := job.ParseCanonical(cjRaw)
	if err != nil {
		return nil, err
	}
	if err := cj.Validate(a.reg); err != nil {
		return nil, err
	}
	if calc, _ := cj.CalcID(); calc != m.CalcID {
		return nil, errors.New("record input does not match its calc_id")
	}
	files := map[string][]byte{}
	refs := []job.FileRef{cj.System.Structure.FileRef}
	for _, p := range cj.System.Pseudopotentials {
		refs = append(refs, p.FileRef)
	}
	for _, r := range refs {
		data, err := os.ReadFile(filepath.Join(dir, "input", r.Filename))
		if err != nil || contentid.SHA256(data) != r.Digest {
			return nil, fmt.Errorf("record input %s missing or altered", r.Filename)
		}
		files[r.Filename] = data
	}
	cj.Delivery.Mode = job.DeliveryAttached
	if req.Detach {
		cj.Delivery.Mode = job.DeliveryDetached
	}
	exclude := map[peer.ID]bool{}
	if w, err := peer.Decode(m.WorkerPeerID); err == nil {
		exclude[w] = true // independent re-execution (§26.1)
	}
	if req.EnergyTolEV <= 0 {
		req.EnergyTolEV = DefaultEnergyTolEV
	}
	if req.ForceTolEVA <= 0 {
		req.ForceTolEVA = DefaultForceTolEVA
	}
	t, _ := json.Marshal(reproTarget{RecordID: req.RecordID, EnergyTolEV: req.EnergyTolEV, ForceTolEVA: req.ForceTolEVA})
	res, err := a.submitPrepared(ctx, &job.Prepared{Canonical: *cj, Files: files}, cjRaw, SubmitRequest{Peer: req.Peer}, exclude,
		func(execID string) error { return a.store.SetKV("reproduces:"+execID, string(t)) }, progress)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// compareReproduction writes reproduction.json after a reproduction result
// has been parsed.
func (a *Agent) compareReproduction(execID, resultDir, parsedDigest string, parsed []byte) {
	raw, ok, _ := a.store.GetKV("reproduces:" + execID)
	if !ok {
		return
	}
	var t reproTarget
	if json.Unmarshal([]byte(raw), &t) != nil {
		return
	}
	_, m, err := a.LocalRecord(t.RecordID)
	if err != nil {
		return
	}
	dir, _ := a.RecordDir(t.RecordID)
	orig, err := os.ReadFile(filepath.Join(dir, "parsed", "result.json"))
	var c *record.Comparison
	if err != nil {
		c = &record.Comparison{Against: t.RecordID, Status: record.StatusInsufficientMetadata}
	} else {
		c = record.Compare(m.ParsedResultDigest, orig, parsedDigest, parsed, t.EnergyTolEV, t.ForceTolEVA, t.RecordID)
	}
	out, _ := json.MarshalIndent(c, "", "  ")
	writeFile(filepath.Join(resultDir, "reproduction.json"), append(out, '\n'))
	a.store.AppendEvent("reproduction", map[string]any{"exec_id": execID, "record_id": t.RecordID, "status": c.Status})
	a.log.Info("reproduction compared", "record", t.RecordID, "status", c.Status, "energy_diff_ev", c.EnergyDiffEV)
}
