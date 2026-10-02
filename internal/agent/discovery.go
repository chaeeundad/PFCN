package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"

	"github.com/chaeeundad/PFCN/internal/protocol"
	"github.com/chaeeundad/PFCN/internal/solver"
	"github.com/chaeeundad/PFCN/internal/store"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

// Discovery (§9, §14): bootstrap peers, a Kademlia DHT in the private /pumat
// protocol namespace, provider records keyed by solver manifest, and mDNS on
// the local network. Provider records are only hints; a fresh signed
// capability query is always authoritative (§11.3).

const (
	dhtPrefix       = "/pumat"
	reprovideEvery  = time.Hour
	findTimeout     = 20 * time.Second
	maxCandidates   = 20
	mdnsServicePref = "pumat"
)

// solverKey is the DHT provider key for a solver manifest in a namespace.
func solverKey(namespace, manifestDigest string) cid.Cid {
	mh, _ := multihash.Sum([]byte("pumat/v1"+namespace+"/solver/"+manifestDigest), multihash.SHA2_256, -1)
	return cid.NewCidV1(cid.Raw, mh)
}

type discovery struct {
	dht      *dht.IpfsDHT
	mu       sync.Mutex
	lanPeers map[peer.ID]time.Time
	reach    network.Reachability
}

func parseAddrInfos(addrs []string) ([]peer.AddrInfo, error) {
	byID := map[peer.ID]*peer.AddrInfo{}
	var order []peer.ID
	for _, s := range addrs {
		ma, err := multiaddr.NewMultiaddr(s)
		if err != nil {
			return nil, fmt.Errorf("invalid address %q: %w", s, err)
		}
		info, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			return nil, fmt.Errorf("address %q must end with /p2p/<peer-id>: %w", s, err)
		}
		if cur, ok := byID[info.ID]; ok {
			cur.Addrs = append(cur.Addrs, info.Addrs...)
			continue
		}
		byID[info.ID] = info
		order = append(order, info.ID)
	}
	out := make([]peer.AddrInfo, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// startDiscovery brings up the DHT, connects to bootstrap peers and starts mDNS.
func (a *Agent) startDiscovery(ctx context.Context) error {
	boot, err := parseAddrInfos(a.cfg.Network.Bootstrap)
	if err != nil {
		return fmt.Errorf("network.bootstrap: %w", err)
	}
	mode := dht.ModeAuto
	switch a.cfg.Network.DHTMode {
	case "server":
		mode = dht.ModeServer
	case "client":
		mode = dht.ModeClient
	}
	d, err := dht.New(a.host,
		dht.ProtocolPrefix(dhtPrefix),
		dht.Mode(mode),
		dht.BootstrapPeers(boot...),
		dht.DisableValues(), // only provider records are used
	)
	if err != nil {
		return fmt.Errorf("dht: %w", err)
	}
	a.disc = &discovery{dht: d, lanPeers: map[peer.ID]time.Time{}}

	for _, b := range boot {
		go func(b peer.AddrInfo) {
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if err := a.host.Connect(cctx, b); err != nil {
				a.log.Warn("bootstrap peer unreachable", "peer", b.ID, "err", err)
			}
		}(b)
	}
	if err := d.Bootstrap(ctx); err != nil {
		a.log.Warn("dht bootstrap", "err", err)
	}

	if a.cfg.Network.MDNS {
		svc := mdns.NewMdnsService(a.host, mdnsServicePref+"-"+contentid.BLAKE3([]byte(a.cfg.Namespace))[7:15], a)
		if err := svc.Start(); err != nil {
			a.log.Warn("mdns", "err", err)
		} else {
			go func() {
				<-ctx.Done()
				svc.Close()
			}()
		}
	}

	sub, err := a.host.EventBus().Subscribe(new(event.EvtLocalReachabilityChanged))
	if err == nil {
		go func() {
			defer sub.Close()
			for {
				select {
				case <-ctx.Done():
					return
				case ev, ok := <-sub.Out():
					if !ok {
						return
					}
					r := ev.(event.EvtLocalReachabilityChanged).Reachability
					a.disc.mu.Lock()
					a.disc.reach = r
					a.disc.mu.Unlock()
					a.log.Info("reachability changed", "reachability", r.String())
				}
			}
		}()
	}

	a.wg.Add(1)
	go a.provideLoop(ctx)
	return nil
}

// HandlePeerFound implements mdns.Notifee.
func (a *Agent) HandlePeerFound(info peer.AddrInfo) {
	if info.ID == a.id.PeerID {
		return
	}
	a.disc.mu.Lock()
	a.disc.lanPeers[info.ID] = time.Now()
	a.disc.mu.Unlock()
	a.host.Peerstore().AddAddrs(info.ID, info.Addrs, time.Hour)
	// Both sides see each other at once; letting only the smaller peer ID dial
	// avoids TCP simultaneous open, which breaks the security handshake.
	if a.id.PeerID >= info.ID {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.host.Connect(ctx, info)
}

// provideLoop announces solver provider records while the node is available.
func (a *Agent) provideLoop(ctx context.Context) {
	defer a.wg.Done()
	t := time.NewTicker(reprovideEvery)
	defer t.Stop()
	a.provide(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.provide(ctx)
		case <-a.provideNow:
			a.provide(ctx)
		}
	}
}

func (a *Agent) provide(ctx context.Context) {
	if a.disc == nil || a.Mode() != ModeAvailable || a.rt == nil {
		return
	}
	cat, err := a.solverCatalog()
	if err != nil {
		return
	}
	for digest, v := range cat {
		if _, ok := v.Manifest.ArtifactFor(a.platform); !ok {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, time.Minute)
		err := a.disc.dht.Provide(pctx, solverKey(a.cfg.Namespace, digest), true)
		cancel()
		if err != nil {
			a.log.Debug("provide failed (will retry)", "solver", v.Manifest.Name, "err", err)
			continue
		}
		a.log.Info("announced solver provider record", "solver", v.Manifest.Name, "digest", digest)
	}
}

// candidate is a verified worker offer.
type candidate struct {
	id     peer.ID
	cap    *protocol.Capability
	direct bool
}

// findCandidates discovers workers for a solver and checks each one's signed
// capability (§14.3). Results are ordered: direct connections first, then by
// available cores.
func (a *Agent) findCandidates(ctx context.Context, sv *solver.Verified, cores, memory int64, exclude map[peer.ID]bool) ([]candidate, error) {
	ids := map[peer.ID]bool{}
	if a.disc != nil {
		a.disc.mu.Lock()
		for id := range a.disc.lanPeers {
			ids[id] = true
		}
		a.disc.mu.Unlock()
		fctx, cancel := context.WithTimeout(ctx, findTimeout)
		for info := range a.disc.dht.FindProvidersAsync(fctx, solverKey(a.cfg.Namespace, sv.Digest), maxCandidates) {
			if info.ID == a.id.PeerID {
				continue
			}
			a.host.Peerstore().AddAddrs(info.ID, info.Addrs, 10*time.Minute)
			ids[info.ID] = true
		}
		cancel()
	}
	delete(ids, a.id.PeerID)
	for id := range exclude {
		delete(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.New("no workers found for this solver (check network.bootstrap, or pass --peer)")
	}

	var mu sync.Mutex
	var out []candidate
	var wg sync.WaitGroup
	for id := range ids {
		wg.Add(1)
		go func(id peer.ID) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := a.host.Connect(cctx, peer.AddrInfo{ID: id}); err != nil {
				return
			}
			c, err := a.QueryCapability(cctx, id)
			if err != nil || c.Status != ModeAvailable || c.CPU.AvailableCores < cores || c.MemoryBytes < memory {
				return
			}
			if _, ok := sv.Manifest.ArtifactFor(c.Platform); !ok {
				return
			}
			mu.Lock()
			out = append(out, candidate{id: id, cap: c, direct: a.isDirect(id)})
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	if len(out) == 0 {
		return nil, fmt.Errorf("%d workers found, none currently eligible (busy, paused, or too small for this job)", len(ids))
	}
	// Rank: direct connectivity, then local reliability (§35.2), then capacity.
	stats, _ := a.store.Stats(store.RoleRequester)
	rel := func(id peer.ID) float64 { return stats[store.RoleRequester+"|"+id.String()].Reliability() }
	sort.Slice(out, func(i, j int) bool {
		if out[i].direct != out[j].direct {
			return out[i].direct
		}
		if ri, rj := rel(out[i].id), rel(out[j].id); ri != rj {
			return ri > rj
		}
		return out[i].cap.CPU.AvailableCores > out[j].cap.CPU.AvailableCores
	})
	return out, nil
}

// isDirect reports whether we have a non-relayed connection to p.
func (a *Agent) isDirect(p peer.ID) bool {
	for _, c := range a.host.Network().ConnsToPeer(p) {
		if !c.Stat().Limited && !strings.Contains(c.RemoteMultiaddr().String(), "p2p-circuit") {
			return true
		}
	}
	return false
}

// peerAddrs returns dialable multiaddrs for p from the peerstore.
func (a *Agent) peerAddrs(p peer.ID) []string {
	var out []string
	for _, ma := range a.host.Peerstore().Addrs(p) {
		out = append(out, ma.String()+"/p2p/"+p.String())
	}
	return out
}

// NetworkView is returned by the diagnose API.
type NetworkView struct {
	PeerID        string   `json:"peer_id"`
	Reachability  string   `json:"reachability"`
	ListenAddrs   []string `json:"listen_addrs"`
	DHTMode       string   `json:"dht_mode"`
	RoutingTable  int      `json:"routing_table_size"`
	Bootstrap     []string `json:"bootstrap"`
	BootstrapUp   []string `json:"bootstrap_connected"`
	LANPeers      int      `json:"lan_peers"`
	ConnectedPeer []string `json:"connected_peers"`
}

// Network returns a diagnostic snapshot (§32.3, §32.7).
func (a *Agent) Network() NetworkView {
	v := NetworkView{PeerID: a.id.PeerID.String(), ListenAddrs: a.Addrs(), DHTMode: a.cfg.Network.DHTMode, Bootstrap: a.cfg.Network.Bootstrap}
	if a.disc != nil {
		a.disc.mu.Lock()
		v.Reachability = a.disc.reach.String()
		v.LANPeers = len(a.disc.lanPeers)
		a.disc.mu.Unlock()
		v.RoutingTable = a.disc.dht.RoutingTable().Size()
	}
	boot, _ := parseAddrInfos(a.cfg.Network.Bootstrap)
	for _, b := range boot {
		if a.host.Network().Connectedness(b.ID) == network.Connected {
			v.BootstrapUp = append(v.BootstrapUp, b.ID.String())
		}
	}
	for _, p := range a.host.Network().Peers() {
		kind := "direct"
		if !a.isDirect(p) {
			kind = "relayed"
		}
		v.ConnectedPeer = append(v.ConnectedPeer, p.String()+" ("+kind+")")
	}
	sort.Strings(v.ConnectedPeer)
	return v
}
