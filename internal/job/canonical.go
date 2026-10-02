package job

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/chaeeundad/PFCN/pkg/canonical"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

const (
	Schema       = "pumat.job.v1alpha1"
	calcIDSchema = "pumat.calc.v1"

	VisibilityPublic   = "public"
	VisibilityUnlisted = "unlisted"
	VisibilityPrivate  = "private"

	DeliveryAttached = "attached"
	DeliveryDetached = "detached"

	DefaultRetentionSeconds = 24 * 3600
)

// Canonical is the normalized job (§7.4.2). Its RFC 8785 encoding is stored
// as input/canonical-job.json in the input bundle.
type Canonical struct {
	Schema      string               `json:"schema"`
	Kind        string               `json:"kind"`
	Metadata    CanonicalMetadata    `json:"metadata"`
	Solver      CanonicalSolver      `json:"solver"`
	Resources   CanonicalResources   `json:"resources"`
	System      CanonicalSystem      `json:"system"`
	Calculation CanonicalCalculation `json:"calculation"`
	Publication CanonicalPublication `json:"publication"`
	Delivery    CanonicalDelivery    `json:"delivery"`
}

type CanonicalMetadata struct {
	Name       string            `json:"name"`
	Visibility string            `json:"visibility"`
	Labels     map[string]string `json:"labels"`
}

type CanonicalSolver struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	ManifestDigest string `json:"manifest_digest"`
	Entrypoint     string `json:"entrypoint"`
}

type CanonicalResources struct {
	CPUCores        int64 `json:"cpu_cores"`
	MemoryBytes     int64 `json:"memory_bytes"`
	WalltimeSeconds int64 `json:"walltime_seconds"`
	GPUCount        int64 `json:"gpu_count"`
}

// FileRef identifies a bundled input file by content.
type FileRef struct {
	Filename string `json:"filename"`
	Digest   string `json:"digest"` // sha256
	Size     int64  `json:"size"`
}

type CanonicalSystem struct {
	Structure        StructureFile         `json:"structure"`
	Pseudopotentials map[string]PseudoFile `json:"pseudopotentials"`
}

type StructureFile struct {
	FileRef
	Format string `json:"format"`
}

type PseudoFile struct {
	FileRef
	Source string `json:"source"`
}

type CanonicalCalculation struct {
	Type       string         `json:"type"`
	Parameters map[string]any `json:"parameters"`
}

type CanonicalPublication struct {
	Input        bool   `json:"input"`
	RawOutput    bool   `json:"raw_output"`
	ParsedOutput bool   `json:"parsed_output"`
	Provenance   bool   `json:"provenance"`
	License      string `json:"license"`
}

type CanonicalDelivery struct {
	Mode                   string `json:"mode"`
	ResultRetentionSeconds int64  `json:"result_retention_seconds"`
	CustodianPeerID        string `json:"custodian_peer_id"`
}

// Adapter supplies solver-specific validation (§39).
type Adapter interface {
	Kind() string
	SolverName() string
	// NormalizeCalculation validates the calculation type and parameters
	// against the solver allowlist and returns canonical values (no floats).
	NormalizeCalculation(Calculation) (CanonicalCalculation, error)
	// ValidateSystem checks bundled files, e.g. structure parse and
	// pseudopotential coverage of every element.
	ValidateSystem(sys CanonicalSystem, files map[string][]byte) error
	// ValidateCanonical re-validates a canonical calculation received from a
	// peer (workers never trust requester-side validation).
	ValidateCanonical(CanonicalCalculation) error
}

// Registry maps job kinds to adapters.
type Registry map[string]Adapter

func NewRegistry(adapters ...Adapter) Registry {
	r := Registry{}
	for _, a := range adapters {
		r[a.Kind()] = a
	}
	return r
}

var (
	nameRe     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	fileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	licenseRe  = regexp.MustCompile(`^[A-Za-z0-9.+-]{1,64}$`)
	elementRe  = regexp.MustCompile(`^[A-Z][a-z]?$`)
)

// Prepared is a normalized job plus the input files to bundle.
type Prepared struct {
	Canonical Canonical
	Files     map[string][]byte // bundle file name -> content
}

// Prepare normalizes a parsed spec. baseDir resolves relative file paths
// (normally the YAML file's directory). Files are read from the requester's
// local disk.
func Prepare(s *Spec, baseDir string, reg Registry, pseudoDirs []string) (*Prepared, error) {
	if s.APIVersion != APIVersion {
		return nil, fmt.Errorf("job: apiVersion must be %q", APIVersion)
	}
	ad, ok := reg[s.Kind]
	if !ok {
		return nil, fmt.Errorf("job: unsupported kind %q", s.Kind)
	}
	c := Canonical{Schema: Schema, Kind: s.Kind}

	// metadata
	if !nameRe.MatchString(s.Metadata.Name) {
		return nil, errors.New("job: metadata.name must be lowercase alphanumeric with dashes, 1-63 chars")
	}
	vis := s.Metadata.Visibility
	if vis == "" {
		vis = VisibilityPrivate
	}
	if vis != VisibilityPublic && vis != VisibilityUnlisted && vis != VisibilityPrivate {
		return nil, fmt.Errorf("job: invalid visibility %q", vis)
	}
	labels := map[string]string{}
	if len(s.Metadata.Labels) > 32 {
		return nil, errors.New("job: at most 32 labels")
	}
	for k, v := range s.Metadata.Labels {
		if !nameRe.MatchString(k) || len(v) > 128 {
			return nil, fmt.Errorf("job: invalid label %q", k)
		}
		labels[k] = v
	}
	c.Metadata = CanonicalMetadata{Name: s.Metadata.Name, Visibility: vis, Labels: labels}

	// solver
	if s.Solver.Name != ad.SolverName() {
		return nil, fmt.Errorf("job: kind %s requires solver %q", s.Kind, ad.SolverName())
	}
	if alg, _, err := contentid.ParseDigest(s.Solver.ManifestDigest); err != nil || alg != contentid.AlgSHA256 {
		return nil, errors.New("job: solver.manifestDigest must be a sha256 digest of the signed solver manifest")
	}
	if s.Solver.Version == "" || s.Solver.Entrypoint == "" {
		return nil, errors.New("job: solver.version and solver.entrypoint are required")
	}
	c.Solver = CanonicalSolver{Name: s.Solver.Name, Version: s.Solver.Version, ManifestDigest: s.Solver.ManifestDigest, Entrypoint: s.Solver.Entrypoint}

	// resources
	if s.Resources.CPU.Cores < 1 || s.Resources.CPU.Cores > 1024 {
		return nil, errors.New("job: resources.cpu.cores must be 1-1024")
	}
	mem, err := ParseSize(s.Resources.Memory)
	if err != nil || mem < 64<<20 {
		return nil, errors.New("job: resources.memory must be at least 64MiB (e.g. 8GiB)")
	}
	wall, err := ParseDuration(s.Resources.Walltime)
	if err != nil || wall < 60 {
		return nil, errors.New("job: resources.walltime must be at least 1m (e.g. 2h)")
	}
	if s.Resources.GPU.Count != 0 {
		return nil, errors.New("job: GPU jobs are not supported yet (Phase 6)")
	}
	c.Resources = CanonicalResources{CPUCores: int64(s.Resources.CPU.Cores), MemoryBytes: mem, WalltimeSeconds: wall}

	// system files
	files := map[string][]byte{}
	sp := s.System.Structure.File
	if sp == "" {
		return nil, errors.New("job: system.structure.file is required")
	}
	sdata, err := os.ReadFile(resolve(baseDir, sp))
	if err != nil {
		return nil, fmt.Errorf("job: read structure: %w", err)
	}
	sname := filepath.Base(sp)
	if !fileNameRe.MatchString(sname) || filepath.Ext(sname) != ".cif" {
		return nil, fmt.Errorf("job: structure file %q must be a .cif with a simple name", sname)
	}
	files[sname] = sdata
	c.System.Structure = StructureFile{FileRef: fileRef(sname, sdata), Format: "cif"}

	if len(s.System.Pseudopotentials) == 0 {
		return nil, errors.New("job: system.pseudopotentials is required")
	}
	c.System.Pseudopotentials = map[string]PseudoFile{}
	for el, p := range s.System.Pseudopotentials {
		if !elementRe.MatchString(el) {
			return nil, fmt.Errorf("job: invalid element symbol %q", el)
		}
		if !fileNameRe.MatchString(p.Filename) {
			return nil, fmt.Errorf("job: invalid pseudopotential filename %q", p.Filename)
		}
		if _, ok := files[p.Filename]; ok {
			return nil, fmt.Errorf("job: duplicate file name %q", p.Filename)
		}
		if alg, _, err := contentid.ParseDigest(p.Digest); err != nil || alg != contentid.AlgSHA256 {
			return nil, fmt.Errorf("job: pseudopotential %s needs a sha256 digest", el)
		}
		data, err := findPseudo(p.Filename, baseDir, pseudoDirs)
		if err != nil {
			return nil, err
		}
		if got := contentid.SHA256(data); got != p.Digest {
			return nil, fmt.Errorf("job: pseudopotential %s digest mismatch: file has %s, job pins %s", p.Filename, got, p.Digest)
		}
		files[p.Filename] = data
		c.System.Pseudopotentials[el] = PseudoFile{FileRef: fileRef(p.Filename, data), Source: p.Source}
	}

	// calculation
	calc, err := ad.NormalizeCalculation(s.Calculation)
	if err != nil {
		return nil, err
	}
	c.Calculation = calc
	if err := ad.ValidateSystem(c.System, files); err != nil {
		return nil, err
	}

	// publication
	pub := s.Publication
	if vis != VisibilityPrivate && !licenseRe.MatchString(pub.License) {
		return nil, errors.New("job: publication.license is required for public and unlisted jobs (e.g. CC-BY-4.0)")
	}
	c.Publication = CanonicalPublication{Input: pub.Input, RawOutput: pub.RawOutput, ParsedOutput: pub.ParsedOutput, Provenance: pub.Provenance, License: pub.License}

	// delivery
	mode := s.Delivery.Mode
	if mode == "" {
		mode = DeliveryAttached
	}
	if mode != DeliveryAttached && mode != DeliveryDetached {
		return nil, fmt.Errorf("job: invalid delivery.mode %q", mode)
	}
	ret := int64(DefaultRetentionSeconds)
	if s.Delivery.ResultRetention != "" {
		if ret, err = ParseDuration(s.Delivery.ResultRetention); err != nil || ret < 300 {
			return nil, errors.New("job: delivery.resultRetention must be at least 5m")
		}
	}
	if s.Delivery.Custodian != nil {
		return nil, errors.New("job: delivery.custodian is not supported yet (planned after Phase 2)")
	}
	c.Delivery = CanonicalDelivery{Mode: mode, ResultRetentionSeconds: ret}

	return &Prepared{Canonical: c, Files: files}, nil
}

// Validate re-checks a canonical job received from a peer.
func (c *Canonical) Validate(reg Registry) error {
	if c.Schema != Schema {
		return fmt.Errorf("job: schema %q", c.Schema)
	}
	ad, ok := reg[c.Kind]
	if !ok {
		return fmt.Errorf("job: unsupported kind %q", c.Kind)
	}
	if c.Solver.Name != ad.SolverName() {
		return errors.New("job: solver/kind mismatch")
	}
	if c.Resources.CPUCores < 1 || c.Resources.MemoryBytes < 64<<20 || c.Resources.WalltimeSeconds < 60 || c.Resources.GPUCount != 0 {
		return errors.New("job: invalid resources")
	}
	if !fileNameRe.MatchString(c.System.Structure.Filename) || c.System.Structure.Format != "cif" {
		return errors.New("job: invalid structure reference")
	}
	for el, p := range c.System.Pseudopotentials {
		if !elementRe.MatchString(el) || !fileNameRe.MatchString(p.Filename) {
			return errors.New("job: invalid pseudopotential reference")
		}
	}
	return ad.ValidateCanonical(c.Calculation)
}

// Marshal returns the RFC 8785 bytes of the canonical job.
func (c *Canonical) Marshal() ([]byte, error) { return canonical.Marshal(c) }

// CalcID derives the logical calculation ID (§12.4). Runtime blocks and file
// names are excluded; files enter by digest only.
func (c *Canonical) CalcID() (string, error) {
	pseudos := map[string]any{}
	var deps []any
	var depList []string
	for el, p := range c.System.Pseudopotentials {
		pseudos[el] = p.Digest
		depList = append(depList, p.Digest)
	}
	sort.Strings(depList)
	for _, d := range depList {
		deps = append(deps, d)
	}
	if deps == nil {
		deps = []any{}
	}
	params := map[string]any{}
	for k, v := range c.Calculation.Parameters {
		params[k] = v
	}
	d, err := contentid.Derive(calcIDSchema, map[string]any{
		"kind":                   c.Kind,
		"solver_manifest_digest": c.Solver.ManifestDigest,
		"entrypoint":             c.Solver.Entrypoint,
		"system": map[string]any{
			"structure":        map[string]any{"digest": c.System.Structure.Digest, "format": c.System.Structure.Format},
			"pseudopotentials": pseudos,
		},
		"calculation":  map[string]any{"type": c.Calculation.Type, "parameters": params},
		"dependencies": deps,
	})
	if err != nil {
		return "", err
	}
	return contentid.Typed(contentid.KindCalc, d), nil
}

// ParseCanonical decodes canonical job bytes (strict).
func ParseCanonical(b []byte) (*Canonical, error) {
	var c Canonical
	if err := canonical.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("job: canonical job: %w", err)
	}
	return &c, nil
}

func fileRef(name string, data []byte) FileRef {
	return FileRef{Filename: name, Digest: contentid.SHA256(data), Size: int64(len(data))}
}

func resolve(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

func findPseudo(name, baseDir string, dirs []string) ([]byte, error) {
	cands := []string{filepath.Join(baseDir, name), filepath.Join(baseDir, "pseudo", name)}
	for _, d := range dirs {
		cands = append(cands, filepath.Join(d, name))
	}
	for _, p := range cands {
		if b, err := os.ReadFile(p); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("job: pseudopotential %s not found (looked in the job directory, ./pseudo, and configured pseudo dirs)", name)
}
