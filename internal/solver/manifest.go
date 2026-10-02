// Package solver handles signed solver manifests and the solver trust policy
// (spec §13).
package solver

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/identity"
	"github.com/chaeeundad/PFCN/internal/job"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

const Schema = "pumat.solver.v1"

// Manifest is the signed solver manifest (canonical JSON form).
type Manifest struct {
	Schema       string                `json:"schema"`
	Name         string                `json:"name"`
	Version      string                `json:"version"`
	Source       Source                `json:"source"`
	Artifacts    []Artifact            `json:"artifacts"`
	IndexDigest  string                `json:"index_digest"`
	Entrypoints  map[string]Entrypoint `json:"entrypoints"`
	Security     Security              `json:"security"`
	Numerics     Numerics              `json:"numerics"`
	Parser       Parser                `json:"parser"`
	Attestations Attestations          `json:"attestations"`
	IssuedAt     string                `json:"issued_at"`
}

type Source struct {
	Repository string `json:"repository" yaml:"repository"`
	Commit     string `json:"commit" yaml:"commit"`
	License    string `json:"license" yaml:"license"`
}

type Artifact struct {
	Platform  string `json:"platform" yaml:"platform"`
	Reference string `json:"reference" yaml:"reference"`
	MediaType string `json:"media_type" yaml:"mediaType"`
	Digest    string `json:"digest" yaml:"digest"`
}

type Entrypoint struct {
	Launcher           string `json:"launcher"`
	Network            string `json:"network"`
	MaxProcesses       int64  `json:"max_processes"`
	MaxWalltimeSeconds int64  `json:"max_walltime_seconds"`
}

type Security struct {
	RootFilesystem string `json:"root_filesystem" yaml:"rootFilesystem"`
	Network        string `json:"network" yaml:"network"`
	Privileged     bool   `json:"privileged" yaml:"privileged"`
	HostMounts     bool   `json:"host_mounts" yaml:"hostMounts"`
}

type Numerics struct {
	BLAS        string `json:"blas" yaml:"blas"`
	BLASVersion string `json:"blas_version" yaml:"blasVersion"`
	CPUDispatch string `json:"cpu_dispatch" yaml:"cpuDispatch"`
}

type Parser struct {
	Name      string `json:"name" yaml:"name"`
	Version   string `json:"version" yaml:"version"`
	Format    string `json:"format" yaml:"format"`
	Reference string `json:"reference" yaml:"reference"`
	Digest    string `json:"digest" yaml:"digest"`
}

type Attestations struct {
	SBOM            string `json:"sbom" yaml:"sbom"`
	BuildProvenance string `json:"build_provenance" yaml:"buildProvenance"`
}

// source is the YAML authoring form (§13.1).
type source struct {
	Schema    string     `yaml:"schema"`
	Name      string     `yaml:"name"`
	Version   string     `yaml:"version"`
	Source    Source     `yaml:"source"`
	Artifacts []Artifact `yaml:"artifacts"`
	Index     *struct {
		Digest string `yaml:"digest"`
	} `yaml:"index"`
	Entrypoints map[string]struct {
		Launcher     string `yaml:"launcher"`
		Network      string `yaml:"network"`
		MaxProcesses int64  `yaml:"maxProcesses"`
		MaxWalltime  string `yaml:"maxWalltime"`
	} `yaml:"entrypoints"`
	Security     Security     `yaml:"security"`
	Numerics     Numerics     `yaml:"numerics"`
	Parser       Parser       `yaml:"parser"`
	Attestations Attestations `yaml:"attestations"`
}

var (
	nameRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	platformRe = regexp.MustCompile(`^linux/(amd64|arm64)$`)
)

// FromYAML converts the authoring form into a manifest ready to sign.
func FromYAML(data []byte, issuedAt time.Time) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var s source
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("solver: %w", err)
	}
	m := &Manifest{
		Schema: s.Schema, Name: s.Name, Version: s.Version, Source: s.Source,
		Artifacts: s.Artifacts, Entrypoints: map[string]Entrypoint{},
		Security: s.Security, Numerics: s.Numerics, Parser: s.Parser,
		Attestations: s.Attestations, IssuedAt: issuedAt.UTC().Format(time.RFC3339),
	}
	if s.Index != nil {
		m.IndexDigest = s.Index.Digest
	}
	for name, e := range s.Entrypoints {
		wall, err := job.ParseDuration(e.MaxWalltime)
		if err != nil {
			return nil, fmt.Errorf("solver: entrypoint %s: %w", name, err)
		}
		m.Entrypoints[name] = Entrypoint{Launcher: e.Launcher, Network: e.Network, MaxProcesses: e.MaxProcesses, MaxWalltimeSeconds: wall}
	}
	return m, m.Validate()
}

// Validate enforces the public-network security floor (§13.1, §17).
func (m *Manifest) Validate() error {
	if m.Schema != Schema {
		return fmt.Errorf("solver: schema must be %q", Schema)
	}
	if !nameRe.MatchString(m.Name) || m.Version == "" {
		return errors.New("solver: invalid name or version")
	}
	if len(m.Artifacts) == 0 {
		return errors.New("solver: at least one artifact is required")
	}
	seen := map[string]bool{}
	for _, a := range m.Artifacts {
		if !platformRe.MatchString(a.Platform) || seen[a.Platform] {
			return fmt.Errorf("solver: invalid or duplicate platform %q", a.Platform)
		}
		seen[a.Platform] = true
		if alg, _, err := contentid.ParseDigest(a.Digest); err != nil || alg != contentid.AlgSHA256 {
			return fmt.Errorf("solver: artifact %s needs a sha256 OCI digest", a.Platform)
		}
		if a.Reference == "" {
			return fmt.Errorf("solver: artifact %s needs a reference", a.Platform)
		}
	}
	if len(m.Entrypoints) == 0 {
		return errors.New("solver: at least one entrypoint is required")
	}
	for name, e := range m.Entrypoints {
		if e.Network != "deny" {
			return fmt.Errorf("solver: entrypoint %s must deny network", name)
		}
		if e.Launcher != "mpi" && e.Launcher != "openmp" && e.Launcher != "serial" {
			return fmt.Errorf("solver: entrypoint %s has invalid launcher %q", name, e.Launcher)
		}
		if e.MaxProcesses < 1 || e.MaxWalltimeSeconds < 60 {
			return fmt.Errorf("solver: entrypoint %s needs maxProcesses and maxWalltime", name)
		}
	}
	sec := m.Security
	if sec.RootFilesystem != "readOnly" || sec.Network != "deny" || sec.Privileged || sec.HostMounts {
		return errors.New("solver: security must be readOnly root, network deny, unprivileged, no host mounts")
	}
	if m.Parser.Format != "wasm" {
		return errors.New("solver: parser format must be wasm")
	}
	if alg, _, err := contentid.ParseDigest(m.Parser.Digest); err != nil || alg != contentid.AlgSHA256 {
		return errors.New("solver: parser needs a sha256 digest")
	}
	return nil
}

// ArtifactFor returns the artifact for a platform such as "linux/arm64".
func (m *Manifest) ArtifactFor(platform string) (Artifact, bool) {
	for _, a := range m.Artifacts {
		if a.Platform == platform {
			return a, true
		}
	}
	return Artifact{}, false
}

// Sign validates and signs a manifest with a solver signing key.
func Sign(m *Manifest, key *identity.Identity) (*envelope.Envelope, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return envelope.Sign(key, Schema, m)
}

// Policy lists the solver signers a node trusts (§13.2).
type Policy struct {
	TrustedSigners []string // peer-ID form of Ed25519 signing keys
	AllowSolvers   []string // empty = any trusted solver
}

// Verified is a manifest whose signature matched the policy.
type Verified struct {
	Manifest Manifest
	Digest   string // sha256 of the signed payload (solver manifest digest)
	Envelope *envelope.Envelope
}

// Verify checks a signed manifest against the policy. It fails closed: an
// empty policy trusts nothing.
func (p Policy) Verify(env *envelope.Envelope) (*Verified, error) {
	if err := env.Verify(Schema); err != nil {
		return nil, err
	}
	trusted := false
	for _, s := range p.TrustedSigners {
		if env.SignedBy(s) {
			trusted = true
			break
		}
	}
	if !trusted {
		return nil, errors.New("solver: manifest is not signed by a trusted solver signer")
	}
	var m Manifest
	if err := env.Decode(&m); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if len(p.AllowSolvers) > 0 {
		ok := false
		for _, s := range p.AllowSolvers {
			ok = ok || s == m.Name
		}
		if !ok {
			return nil, fmt.Errorf("solver: %s is not in the local solver allowlist", m.Name)
		}
	}
	return &Verified{Manifest: m, Digest: env.SHA256(), Envelope: env}, nil
}

// LoadFile reads a signed manifest envelope from disk.
func LoadFile(path string) (*envelope.Envelope, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return envelope.ParseFile(b)
}
