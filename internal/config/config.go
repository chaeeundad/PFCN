// Package config loads the node configuration and local resource policy
// (spec §27).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"

	"github.com/chaeeundad/PFCN/internal/job"
)

// DefaultNamespace is the public network namespace (§10).
const DefaultNamespace = "/pumat/public/v1"

// ProjectSolverSigner is the trust root for solver manifests published by the
// Pumat project during the alpha (§13.2, ADR-0009).
const ProjectSolverSigner = "12D3KooWNmqy7RJXuA8VKQDgfcWhhAtiVPpwXGRQA3K2VvaDRKeP"

// Config is the YAML node configuration.
type Config struct {
	Namespace   string      `yaml:"namespace"`
	Listen      []string    `yaml:"listen"`
	Network     Network     `yaml:"network"`
	Resources   Resources   `yaml:"resources"`
	Jobs        Jobs        `yaml:"jobs"`
	Trust       Trust       `yaml:"trust"`
	Solvers     Solvers     `yaml:"solvers"`
	Runtime     Runtime     `yaml:"runtime"`
	Requester   Requester   `yaml:"requester"`
	Publication Publication `yaml:"publication"`
	Indexer     Indexer     `yaml:"indexer"`
}

// Publication configures where public record announcements are pushed (§45).
type Publication struct {
	// Indexers receive announcements directly (in addition to GossipSub).
	Indexers []string `yaml:"indexers"`
}

// Indexer runs the optional explorer/indexer inside the agent (§25).
type Indexer struct {
	Enabled bool   `yaml:"enabled"`
	HTTP    string `yaml:"http"` // listen address, e.g. 127.0.0.1:8080
}

// Network configures discovery and NAT traversal (§9).
type Network struct {
	// Bootstrap peers (multiaddrs ending in /p2p/<id>). Community-operated
	// bootstrap peers are equally valid (§9.1).
	Bootstrap []string `yaml:"bootstrap"`
	// DHTMode is auto, server or client. Bootstrap and relay nodes use server.
	DHTMode string `yaml:"dhtMode"`
	// RelayService offers bounded Circuit Relay v2 reservations to others (§9.4).
	RelayService bool `yaml:"relayService"`
	// StaticRelays are relays used for AutoRelay when this node is not publicly reachable.
	StaticRelays []string `yaml:"staticRelays"`
	// MDNS discovers peers on the local network.
	MDNS bool `yaml:"mdns"`
}

type Resources struct {
	CPU    int    `yaml:"cpu"`
	Memory string `yaml:"memory"`
	Disk   string `yaml:"disk"`
}

type Jobs struct {
	MaxWalltime        string   `yaml:"maxWalltime"`
	MaxConcurrent      int      `yaml:"maxConcurrent"`
	MaxResultRetention string   `yaml:"maxResultRetention"`
	MaxInputBytes      string   `yaml:"maxInputBytes"`
	MaxOutputBytes     string   `yaml:"maxOutputBytes"`
	AcceptVisibility   []string `yaml:"acceptVisibility"`
	// NewRequesterMaxWalltime caps jobs from requesters with fewer than
	// TrustedAfter completed receipts on this node (§35.3).
	NewRequesterMaxWalltime string `yaml:"newRequesterMaxWalltime"`
	TrustedAfter            int    `yaml:"trustedAfter"`
}

type Trust struct {
	SolverSigners []string `yaml:"solverSigners"`
	// RevocationURL is an https URL serving the signed revocation list (§13.5).
	RevocationURL string `yaml:"revocationURL"`
	// RequireRevocationList refuses new work when the list is missing or
	// stale (fail closed). Enable once the namespace publishes a list.
	RequireRevocationList bool `yaml:"requireRevocationList"`
}

type Solvers struct {
	Allow []string `yaml:"allow"`
}

type Runtime struct {
	Engine string `yaml:"engine"` // auto | podman | docker
}

type Requester struct {
	ResultsDir string   `yaml:"resultsDir"`
	PseudoDirs []string `yaml:"pseudoDirs"`
}

// Policy is the parsed, validated form used by the agent.
type Policy struct {
	CPUCores                  int64
	MemoryBytes               int64
	DiskBytes                 int64
	MaxWalltimeSeconds        int64
	MaxConcurrent             int
	MaxResultRetentionSeconds int64
	MaxInputBytes             int64
	MaxOutputBytes            int64
	AcceptVisibility          []string
	NewRequesterMaxWalltime   int64
	TrustedAfter              int
}

// DefaultBootstrap lists project-operated bootstrap peers. It is empty until
// bootstrap1/2.pumat.org are deployed (see docs/DEVLOG.md); add community or
// institutional bootstrap peers in config.yaml meanwhile.
var DefaultBootstrap = []string{}

// Default returns a configuration for a machine with the given resources:
// half the cores and a quarter of memory are offered by default.
func Default(totalCores int, totalMemory int64) *Config {
	cpu := max(totalCores/2, 1)
	memGiB := max(totalMemory/(4<<30), 1)
	return &Config{
		Namespace: DefaultNamespace,
		Listen:    []string{"/ip4/0.0.0.0/udp/4001/quic-v1", "/ip4/0.0.0.0/tcp/4001", "/ip6/::/udp/4001/quic-v1", "/ip6/::/tcp/4001"},
		Network:   Network{Bootstrap: DefaultBootstrap, DHTMode: "auto", MDNS: true},
		Resources: Resources{CPU: cpu, Memory: fmt.Sprintf("%dGiB", memGiB), Disk: "50GiB"},
		Jobs: Jobs{
			MaxWalltime: "6h", MaxConcurrent: 1, MaxResultRetention: "72h",
			MaxInputBytes: "4GiB", MaxOutputBytes: "10GiB",
			AcceptVisibility:        []string{job.VisibilityPublic, job.VisibilityUnlisted, job.VisibilityPrivate},
			NewRequesterMaxWalltime: "2h", TrustedAfter: 3,
		},
		Trust:     Trust{SolverSigners: []string{ProjectSolverSigner}},
		Solvers:   Solvers{Allow: []string{"quantum-espresso"}},
		Runtime:   Runtime{Engine: "auto"},
		Requester: Requester{ResultsDir: defaultResultsDir()},
	}
}

func defaultResultsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "results"
	}
	return filepath.Join(home, "pumat", "results")
}

// Load reads a strict YAML config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if _, err := c.Policy(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the config with 0600 permissions.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	header := []byte("# Pumat node configuration (spec §27). Restart the agent after editing.\n")
	return os.WriteFile(path, append(header, data...), 0o600)
}

// Policy validates and converts resource limits.
func (c *Config) Policy() (*Policy, error) {
	p := &Policy{CPUCores: int64(c.Resources.CPU), MaxConcurrent: c.Jobs.MaxConcurrent, AcceptVisibility: c.Jobs.AcceptVisibility}
	if p.CPUCores < 1 || p.CPUCores > int64(runtime.NumCPU()) {
		return nil, fmt.Errorf("resources.cpu must be 1-%d", runtime.NumCPU())
	}
	if p.MaxConcurrent < 1 {
		return nil, errors.New("jobs.maxConcurrent must be at least 1")
	}
	var err error
	sizes := []struct {
		name, val string
		dst       *int64
	}{
		{"resources.memory", c.Resources.Memory, &p.MemoryBytes},
		{"resources.disk", c.Resources.Disk, &p.DiskBytes},
		{"jobs.maxInputBytes", c.Jobs.MaxInputBytes, &p.MaxInputBytes},
		{"jobs.maxOutputBytes", c.Jobs.MaxOutputBytes, &p.MaxOutputBytes},
	}
	for _, s := range sizes {
		if *s.dst, err = job.ParseSize(s.val); err != nil || *s.dst <= 0 {
			return nil, fmt.Errorf("%s: invalid size %q", s.name, s.val)
		}
	}
	if p.MaxWalltimeSeconds, err = job.ParseDuration(c.Jobs.MaxWalltime); err != nil {
		return nil, fmt.Errorf("jobs.maxWalltime: %w", err)
	}
	p.NewRequesterMaxWalltime = p.MaxWalltimeSeconds
	if c.Jobs.NewRequesterMaxWalltime != "" {
		if p.NewRequesterMaxWalltime, err = job.ParseDuration(c.Jobs.NewRequesterMaxWalltime); err != nil {
			return nil, fmt.Errorf("jobs.newRequesterMaxWalltime: %w", err)
		}
	}
	p.TrustedAfter = c.Jobs.TrustedAfter
	if p.MaxResultRetentionSeconds, err = job.ParseDuration(c.Jobs.MaxResultRetention); err != nil {
		return nil, fmt.Errorf("jobs.maxResultRetention: %w", err)
	}
	switch c.Network.DHTMode {
	case "", "auto", "server", "client":
	default:
		return nil, fmt.Errorf("network.dhtMode must be auto, server or client")
	}
	if c.Runtime.Engine != "auto" && c.Runtime.Engine != "podman" && c.Runtime.Engine != "docker" {
		return nil, fmt.Errorf("runtime.engine must be auto, podman or docker")
	}
	return p, nil
}
