// Package agent is the Pumat node daemon: it owns identity, the libp2p host,
// persisted state, worker execution, and requester result fetching
// (spec §6, §32.0).
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/chaeeundad/PFCN/internal/capability"
	"github.com/chaeeundad/PFCN/internal/config"
	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/identity"
	"github.com/chaeeundad/PFCN/internal/job"
	"github.com/chaeeundad/PFCN/internal/protocol"
	"github.com/chaeeundad/PFCN/internal/record"
	"github.com/chaeeundad/PFCN/internal/sandbox"
	"github.com/chaeeundad/PFCN/internal/solver"
	"github.com/chaeeundad/PFCN/internal/solver/qe"
	"github.com/chaeeundad/PFCN/internal/store"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

// Node modes (§5.4).
const (
	ModePaused    = "paused"
	ModeAvailable = "available"
	ModeDraining  = "draining"
)

// Paths is the on-disk layout under the Pumat home directory (§7.3).
type Paths struct {
	Home string
}

func (p Paths) Identity() string { return filepath.Join(p.Home, "identity") }
func (p Paths) Config() string   { return filepath.Join(p.Home, "config.yaml") }
func (p Paths) DB() string       { return filepath.Join(p.Home, "db", "pumat.sqlite") }
func (p Paths) Work() string     { return filepath.Join(p.Home, "work") }
func (p Paths) Keys() string     { return filepath.Join(p.Home, "keys") }
func (p Paths) Solvers() string  { return filepath.Join(p.Home, "solvers") }

// Socket returns the local API socket path. Unix socket paths are limited to
// ~104 bytes, so long home paths fall back to a hashed name in the temp dir.
func (p Paths) Socket() string {
	s := filepath.Join(p.Home, "agent.sock")
	if len(s) <= 100 {
		return s
	}
	abs, _ := filepath.Abs(p.Home)
	return filepath.Join(os.TempDir(), "pumat-"+contentid.BLAKE3([]byte(abs))[7:19]+".sock")
}
func (p Paths) Log() string { return filepath.Join(p.Home, "logs", "agent.log") }

// ExecDir returns the per-execution work directory.
func (p Paths) ExecDir(execID string) string {
	return filepath.Join(p.Work(), execDirName(execID))
}

func execDirName(execID string) string {
	_, rest, _ := cutLast(execID, ":")
	return rest
}

func cutLast(s, sep string) (string, string, bool) {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return "", s, false
}

// Options configures an agent. Zero values use the config file.
type Options struct {
	Home    string
	Runtime sandbox.Runtime // nil = detect podman/docker
	Logger  *slog.Logger
	// FetchInterval overrides the detached fetch interval (tests).
	FetchInterval time.Duration
}

// Agent is a running node.
type Agent struct {
	paths    Paths
	cfg      *config.Config
	policy   *config.Policy
	id       *identity.Identity
	store    *store.Store
	host     host.Host
	rt       sandbox.Runtime
	platform string
	hw       capability.Hardware
	reg      job.Registry
	trust    solver.Policy
	log      *slog.Logger

	fetchInterval time.Duration

	disc        *discovery
	provideNow  chan struct{}
	stop        context.CancelFunc
	revocations *solver.RevocationList
	topic       *pubsub.Topic
	onAnnounce  func(*record.Announcement, *envelope.Envelope, peer.ID)
	extraAPI    []func(*http.ServeMux)

	mu       sync.Mutex
	mode     string
	reserved int // offered leases awaiting countersignature
	cancels  map[string]context.CancelFunc
	fetching map[string]bool
	wake     chan struct{}
	wg       sync.WaitGroup
}

// Open loads identity, config and state. It does not start networking.
func Open(o Options) (*Agent, error) {
	paths := Paths{Home: o.Home}
	if !identity.Exists(paths.Identity()) {
		return nil, fmt.Errorf("no identity in %s; run `pumat init` first", o.Home)
	}
	id, err := identity.Load(paths.Identity())
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(paths.Config())
	if err != nil {
		return nil, err
	}
	pol, err := cfg.Policy()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(paths.DB(), id)
	if err != nil {
		return nil, err
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	a := &Agent{
		paths: paths, cfg: cfg, policy: pol, id: id, store: st,
		hw:  capability.Detect(),
		reg: job.NewRegistry(qe.Adapter{}),
		trust: solver.Policy{
			TrustedSigners: cfg.Trust.SolverSigners,
			AllowSolvers:   cfg.Solvers.Allow,
		},
		log:           log.With("peer", id.PeerID.String()[len(id.PeerID.String())-6:]),
		fetchInterval: 5 * time.Minute,
		cancels:       map[string]context.CancelFunc{},
		fetching:      map[string]bool{},
		wake:          make(chan struct{}, 1),
		provideNow:    make(chan struct{}, 1),
	}
	if o.FetchInterval > 0 {
		a.fetchInterval = o.FetchInterval
	}
	a.rt = o.Runtime
	if err := a.loadRevocations(); err != nil {
		return nil, err
	}
	mode, ok, err := st.GetKV("mode")
	if err != nil {
		return nil, err
	}
	if !ok {
		mode = ModePaused
	}
	a.mode = mode
	return a, nil
}

// Config returns the loaded configuration.
func (a *Agent) Config() *config.Config { return a.cfg }

// Home returns the Pumat home directory.
func (a *Agent) Home() string { return a.paths.Home }

// PeerID returns the node's peer ID.
func (a *Agent) PeerID() peer.ID { return a.id.PeerID }

// Store exposes the state store (CLI read-only commands).
func (a *Agent) Store() *store.Store { return a.store }

// Addrs returns full dialable multiaddrs including /p2p/<id>.
func (a *Agent) Addrs() []string {
	if a.host == nil {
		return nil
	}
	var out []string
	for _, ma := range a.host.Addrs() {
		out = append(out, ma.String()+"/p2p/"+a.id.PeerID.String())
	}
	return out
}

// Start brings up networking, protocol handlers and background loops. They
// stop when ctx is done or Close is called.
func (a *Agent) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	a.stop = cancel
	if a.rt == nil {
		rt, err := sandbox.Detect(a.cfg.Runtime.Engine)
		if err != nil {
			a.log.Warn("no container runtime; this node can submit jobs but cannot execute them", "err", err)
		} else {
			a.rt = rt
		}
	}
	if a.rt != nil {
		pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		p, err := a.rt.Platform(pctx)
		cancel()
		if err != nil {
			a.log.Warn("container runtime unavailable", "engine", a.rt.Name(), "err", err)
			a.rt = nil
		} else {
			a.platform = p
		}
	}

	// NAT traversal (§9.3): direct, then hole punching (DCUtR), then relay.
	opts := []libp2p.Option{
		libp2p.Identity(a.id.PrivKey),
		libp2p.ListenAddrStrings(a.cfg.Listen...),
		libp2p.NATPortMap(),
		libp2p.EnableHolePunching(),
	}
	if relays, err := parseAddrInfos(a.cfg.Network.StaticRelays); err != nil {
		return fmt.Errorf("network.staticRelays: %w", err)
	} else if len(relays) > 0 {
		opts = append(opts, libp2p.EnableAutoRelayWithStaticRelays(relays))
	}
	if a.cfg.Network.RelayService {
		// Bounded reservations (§9.4); bulk transfers should use direct paths.
		opts = append(opts, libp2p.EnableRelayService(), libp2p.EnableNATService())
	}
	h, err := libp2p.New(opts...)
	if err != nil {
		return fmt.Errorf("agent: libp2p: %w", err)
	}
	a.host = h
	h.SetStreamHandler(protocol.ProtoCapability, a.handleCapability)
	h.SetStreamHandler(protocol.ProtoLease, a.handleLease)
	h.SetStreamHandler(protocol.ProtoInput, a.handleInput)
	h.SetStreamHandler(protocol.ProtoResult, a.handleResult)

	if err := a.recoverWorker(); err != nil {
		return err
	}
	if err := a.startDiscovery(ctx); err != nil {
		return err
	}
	if err := a.startRecords(ctx); err != nil {
		return err
	}
	a.wg.Add(3)
	go a.sweepLoop(ctx)
	go a.fetchLoop(ctx)
	go a.revocationLoop(ctx)
	a.log.Info("agent started", "mode", a.Mode(), "addrs", a.Addrs(), "runtime", a.runtimeName(), "platform", a.platform)
	return nil
}

// Close stops networking and waits for background work.
func (a *Agent) Close() error {
	if a.stop != nil {
		a.stop()
	}
	a.mu.Lock()
	for _, c := range a.cancels {
		c()
	}
	a.mu.Unlock()
	var err error
	if a.disc != nil {
		err = a.disc.dht.Close()
	}
	if a.host != nil {
		err = errors.Join(err, a.host.Close())
	}
	a.wg.Wait()
	return errors.Join(err, a.store.Close())
}

func (a *Agent) runtimeName() string {
	if a.rt == nil {
		return "none"
	}
	return a.rt.Name()
}

// Mode returns the current contribution mode.
func (a *Agent) Mode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

// SetMode switches contribution on/off. Paused stops accepting new leases;
// running jobs continue (drain semantics, §5.3).
func (a *Agent) SetMode(mode string) error {
	if mode != ModePaused && mode != ModeAvailable && mode != ModeDraining {
		return fmt.Errorf("invalid mode %q", mode)
	}
	if mode == ModeAvailable && a.rt == nil {
		return errors.New("cannot become available: no working container runtime (run `pumat doctor`)")
	}
	a.mu.Lock()
	a.mode = mode
	a.mu.Unlock()
	if err := a.store.SetKV("mode", mode); err != nil {
		return err
	}
	if mode == ModeAvailable {
		select {
		case a.provideNow <- struct{}{}:
		default:
		}
	}
	return a.store.AppendEvent("mode", map[string]any{"mode": mode})
}

// connect dials a peer from multiaddr strings.
func (a *Agent) connect(ctx context.Context, addrs []string) (peer.ID, error) {
	var infos []peer.AddrInfo
	for _, s := range addrs {
		ma, err := multiaddr.NewMultiaddr(s)
		if err != nil {
			return "", fmt.Errorf("invalid peer address %q: %w", s, err)
		}
		info, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			return "", fmt.Errorf("peer address %q must end with /p2p/<peer-id>: %w", s, err)
		}
		infos = append(infos, *info)
	}
	if len(infos) == 0 {
		return "", errors.New("no peer address")
	}
	merged := peer.AddrInfo{ID: infos[0].ID}
	for _, i := range infos {
		if i.ID != merged.ID {
			return "", errors.New("addresses refer to different peers")
		}
		merged.Addrs = append(merged.Addrs, i.Addrs...)
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := a.host.Connect(cctx, merged)
	if err != nil && cctx.Err() == nil {
		// One retry covers transient dial races (e.g. simultaneous open).
		time.Sleep(time.Duration(200+time.Now().UnixNano()%300) * time.Millisecond)
		if a.host.Network().Connectedness(merged.ID) == network.Connected {
			err = nil
		} else {
			err = a.host.Connect(network.WithForceDirectDial(cctx, "retry after dial race"), merged)
		}
	}
	if err != nil && a.disc != nil {
		// Addresses may be stale (roaming laptop, new NAT mapping): ask the DHT.
		if info, ferr := a.disc.dht.FindPeer(cctx, merged.ID); ferr == nil {
			err = a.host.Connect(cctx, info)
		}
	}
	if err != nil {
		return "", fmt.Errorf("connect %s: %w", merged.ID, err)
	}
	return merged.ID, nil
}

// solverCatalog loads every signed manifest in the local solver directory.
func (a *Agent) solverCatalog() (map[string]*solver.Verified, error) {
	out := map[string]*solver.Verified{}
	entries, err := os.ReadDir(a.paths.Solvers())
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		env, err := solver.LoadFile(filepath.Join(a.paths.Solvers(), e.Name()))
		if err != nil {
			a.log.Warn("skipping unreadable solver manifest", "file", e.Name(), "err", err)
			continue
		}
		v, err := a.Trust().Verify(env)
		if err != nil {
			a.log.Warn("skipping untrusted solver manifest", "file", e.Name(), "err", err)
			continue
		}
		out[v.Digest] = v
	}
	return out, nil
}

// AddSolver verifies a signed manifest and installs it in the local catalog.
func (a *Agent) AddSolver(env *envelope.Envelope) (*solver.Verified, error) {
	return InstallSolver(a.paths, a.Trust(), env)
}

// InstallSolver verifies and stores a manifest without a running agent.
func InstallSolver(p Paths, trust solver.Policy, env *envelope.Envelope) (*solver.Verified, error) {
	v, err := trust.Verify(env)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p.Solvers(), 0o700); err != nil {
		return nil, err
	}
	raw, err := env.MarshalFile()
	if err != nil {
		return nil, err
	}
	name := v.Manifest.Name + "-" + v.Manifest.Version + "-" + v.Digest[7:19] + ".json"
	return v, os.WriteFile(filepath.Join(p.Solvers(), name), raw, 0o644)
}

// Trust returns the solver trust policy.
func (a *Agent) Trust() solver.Policy {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.trust
}

// SetTrust replaces the solver trust policy.
func (a *Agent) SetTrust(p solver.Policy) {
	a.mu.Lock()
	a.trust = p
	a.mu.Unlock()
}

// nudge wakes the fetch loop.
func (a *Agent) nudge() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}
