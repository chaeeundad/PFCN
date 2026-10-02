// Package record builds and verifies public scientific records (spec §22,
// §45) and compares reproductions (§26).
package record

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/identity"
	"github.com/chaeeundad/PFCN/internal/job"
	"github.com/chaeeundad/PFCN/internal/protocol"
	"github.com/chaeeundad/PFCN/internal/qeparse"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

const (
	Schema             = "pumat.record.v1"
	AnnouncementSchema = "pumat.publication.v1"
)

// Reproducibility statuses (§26.3).
const (
	StatusUnverified           = "UNVERIFIED"
	StatusReproducedExact      = "REPRODUCED_EXACT"
	StatusReproducedTolerance  = "REPRODUCED_WITHIN_TOLERANCE"
	StatusDivergent            = "DIVERGENT"
	StatusInsufficientMetadata = "INSUFFICIENT_METADATA"
)

// FileEntry is one file of a record bundle.
type FileEntry struct {
	Path   string `json:"path"`
	Digest string `json:"digest"` // sha256
	Size   int64  `json:"size"`
}

// Summary holds searchable metadata. Numbers are decimal strings because
// signed payloads carry no floats (§7.4.1).
type Summary struct {
	Calculation string   `json:"calculation"`
	Formula     string   `json:"formula"`
	Elements    []string `json:"elements"`
	NAtoms      int64    `json:"n_atoms"`
	Converged   bool     `json:"converged"`
	EnergyEV    string   `json:"energy_ev"`
	FermiEV     string   `json:"fermi_ev"`
	Ecutwfc     string   `json:"ecutwfc_ry"`
	Kpoints     string   `json:"kpoints"`
}

// Comparison is a reproducer's signed comparison with an earlier record.
type Comparison struct {
	Against         string `json:"against"`
	Status          string `json:"status"`
	EnergyDiffEV    string `json:"energy_diff_ev"`
	EnergyTolEV     string `json:"energy_tolerance_ev"`
	MaxForceDiffEVA string `json:"max_force_diff_ev_per_angstrom"`
	ForceTolEVA     string `json:"force_tolerance_ev_per_angstrom"`
}

// Manifest is the publisher-signed publication manifest (§22.2).
type Manifest struct {
	Schema               string      `json:"schema"`
	Namespace            string      `json:"namespace"`
	CalcID               string      `json:"calc_id"`
	ExecID               string      `json:"exec_id"`
	ReceiptID            string      `json:"receipt_id"`
	Solver               string      `json:"solver"`
	SolverVersion        string      `json:"solver_version"`
	SolverManifestDigest string      `json:"solver_manifest_digest"`
	ParserDigest         string      `json:"parser_digest"`
	ParsedResultDigest   string      `json:"parsed_result_digest"`
	Visibility           string      `json:"visibility"`
	License              string      `json:"license"`
	PublisherPeerID      string      `json:"publisher_peer_id"`
	WorkerPeerID         string      `json:"worker_peer_id"`
	CreatedAt            string      `json:"created_at"`
	Summary              Summary     `json:"summary"`
	Reproduces           string      `json:"reproduces"`
	Comparison           *Comparison `json:"comparison"`
	Files                []FileEntry `json:"files"`
}

// Announcement is the small signed message gossiped after publication (§45).
type Announcement struct {
	Schema               string `json:"schema"`
	RecordID             string `json:"record_id"`
	CalcID               string `json:"calc_id"`
	ExecID               string `json:"exec_id"`
	ReceiptID            string `json:"receipt_id"`
	SolverManifestDigest string `json:"solver_manifest_digest"`
	Solver               string `json:"solver"`
	Visibility           string `json:"visibility"`
	License              string `json:"license"`
	CreatedAt            string `json:"created_at"`
	PublisherPeerID      string `json:"publisher_peer_id"`
}

// ID returns the record ID of a signed manifest.
func ID(env *envelope.Envelope) string { return contentid.Typed(contentid.KindRecord, env.Digest()) }

// Hex returns the digest hex of a record ID (used for directory names).
func Hex(recordID string) (string, error) {
	d, err := contentid.ParseTyped(recordID, contentid.KindRecord)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(d, "blake3:"), nil
}

// Inputs collects what Build needs from a completed requester execution.
type Inputs struct {
	ResultDir      string
	Namespace      string
	Lease          *envelope.Envelope
	Completion     *envelope.Envelope
	Acceptance     *envelope.Envelope
	SolverManifest *envelope.Envelope
	SolverName     string
	SolverVersion  string
	Reproduces     string
	Comparison     *Comparison
}

// Build signs a publication manifest for a completed execution and copies
// the published files into destRoot/<record hex>/. It honours the job's
// publication flags and refuses private jobs (§22.3).
func Build(in Inputs, publisher *identity.Identity, destRoot string) (*envelope.Envelope, error) {
	if in.Acceptance == nil || in.Completion == nil || in.Lease == nil {
		return nil, errors.New("record: only acknowledged executions can be published")
	}
	vr, err := protocol.VerifyReceipt(in.Lease, in.Completion, in.Acceptance)
	if err != nil {
		return nil, err
	}
	if vr.State != "completed" {
		return nil, fmt.Errorf("record: receipt is %s", vr.State)
	}
	if vr.Lease.RequesterPeerID != publisher.PeerID.String() {
		return nil, errors.New("record: only the requester can publish an execution")
	}
	cjRaw, err := os.ReadFile(filepath.Join(in.ResultDir, "input", "canonical-job.json"))
	if err != nil {
		return nil, err
	}
	cj, err := job.ParseCanonical(cjRaw)
	if err != nil {
		return nil, err
	}
	if cj.Metadata.Visibility == job.VisibilityPrivate {
		return nil, errors.New("record: private jobs are never published; change visibility in the job to publish")
	}
	parsed, err := os.ReadFile(filepath.Join(in.ResultDir, "parsed", "result.json"))
	if err != nil {
		return nil, err
	}
	if contentid.BLAKE3(parsed) != vr.Acceptance.ParsedResultDigest {
		return nil, errors.New("record: parsed result does not match the signed acceptance")
	}

	pub := cj.Publication
	include := func(rel string) bool {
		switch {
		case strings.HasPrefix(rel, "input/"):
			return pub.Input && rel != "input/job.yaml" // job.yaml may contain local comments/paths
		case strings.HasPrefix(rel, "output/"):
			return pub.RawOutput
		case strings.HasPrefix(rel, "parsed/"):
			return pub.ParsedOutput
		case strings.HasPrefix(rel, "provenance/"):
			return pub.Provenance
		}
		return false
	}
	var files []FileEntry
	err = filepath.WalkDir(in.ResultDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(in.ResultDir, p)
		rel = filepath.ToSlash(rel)
		if !include(rel) || !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files = append(files, FileEntry{Path: rel, Digest: contentid.SHA256(data), Size: int64(len(data))})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	var res qeparse.Result
	json.Unmarshal(parsed, &res)
	m := Manifest{
		Schema: Schema, Namespace: in.Namespace, CalcID: vr.Completion.CalcID, ExecID: vr.Completion.ExecID,
		ReceiptID: vr.ReceiptID, Solver: in.SolverName, SolverVersion: in.SolverVersion,
		SolverManifestDigest: vr.Completion.SolverManifestDigest, ParserDigest: vr.Acceptance.ParserDigest,
		ParsedResultDigest: vr.Acceptance.ParsedResultDigest, Visibility: cj.Metadata.Visibility,
		License: pub.License, PublisherPeerID: publisher.PeerID.String(), WorkerPeerID: vr.Completion.WorkerPeerID,
		CreatedAt: protocol.Now(time.Now()), Summary: summarize(cj, &res),
		Reproduces: in.Reproduces, Comparison: in.Comparison, Files: files,
	}
	env, err := envelope.Sign(publisher, Schema, m)
	if err != nil {
		return nil, err
	}
	hex, _ := Hex(ID(env))
	dir := filepath.Join(destRoot, hex)
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(in.ResultDir, filepath.FromSlash(f.Path)))
		if err != nil {
			return nil, err
		}
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(f.Path)), data); err != nil {
			return nil, err
		}
	}
	if err := writeChecksums(dir, files); err != nil {
		return nil, err
	}
	raw, _ := env.MarshalFile()
	return env, writeFile(filepath.Join(dir, "manifest.json"), raw)
}

// Announce creates the signed announcement for a record.
func Announce(env *envelope.Envelope, publisher *identity.Identity) (*envelope.Envelope, error) {
	var m Manifest
	if err := env.Decode(&m); err != nil {
		return nil, err
	}
	return envelope.Sign(publisher, AnnouncementSchema, Announcement{
		Schema: AnnouncementSchema, RecordID: ID(env), CalcID: m.CalcID, ExecID: m.ExecID,
		ReceiptID: m.ReceiptID, SolverManifestDigest: m.SolverManifestDigest, Solver: m.Solver,
		Visibility: m.Visibility, License: m.License, CreatedAt: m.CreatedAt, PublisherPeerID: m.PublisherPeerID,
	})
}

// VerifyAnnouncement checks an announcement's signature and shape.
func VerifyAnnouncement(env *envelope.Envelope) (*Announcement, error) {
	if err := env.Verify(AnnouncementSchema); err != nil {
		return nil, err
	}
	var a Announcement
	if err := env.Decode(&a); err != nil {
		return nil, err
	}
	if err := env.VerifySigners(AnnouncementSchema, a.PublisherPeerID); err != nil {
		return nil, err
	}
	if _, err := Hex(a.RecordID); err != nil {
		return nil, err
	}
	return &a, nil
}

var safePath = regexp.MustCompile(`^(input|output|parsed|provenance)/[A-Za-z0-9._+-]{1,128}$`)

// VerifyManifest checks the manifest signature and structure. It does not
// read files; use VerifyFiles for that.
func VerifyManifest(env *envelope.Envelope) (*Manifest, error) {
	if err := env.Verify(Schema); err != nil {
		return nil, err
	}
	var m Manifest
	if err := env.Decode(&m); err != nil {
		return nil, err
	}
	if err := env.VerifySigners(Schema, m.PublisherPeerID); err != nil {
		return nil, err
	}
	if m.Visibility != job.VisibilityPublic && m.Visibility != job.VisibilityUnlisted {
		return nil, errors.New("record: invalid visibility")
	}
	for _, f := range m.Files {
		if !safePath.MatchString(f.Path) || f.Size < 0 {
			return nil, fmt.Errorf("record: unsafe file path %q", f.Path)
		}
		if alg, _, err := contentid.ParseDigest(f.Digest); err != nil || alg != contentid.AlgSHA256 {
			return nil, fmt.Errorf("record: invalid digest for %s", f.Path)
		}
	}
	return &m, nil
}

// VerifyFiles checks every file digest in dir, and, when provenance is
// included, the full receipt chain and its binding to the manifest.
func VerifyFiles(m *Manifest, dir string) error {
	for _, f := range m.Files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			return fmt.Errorf("record: %s missing", f.Path)
		}
		if int64(len(data)) != f.Size || contentid.SHA256(data) != f.Digest {
			return fmt.Errorf("record: %s does not match its digest", f.Path)
		}
	}
	if m.has("parsed/result.json") {
		data, _ := os.ReadFile(filepath.Join(dir, "parsed", "result.json"))
		if contentid.BLAKE3(data) != m.ParsedResultDigest {
			return errors.New("record: parsed result digest mismatch")
		}
	}
	if m.has("provenance/lease.json") && m.has("provenance/receipt-completion.json") && m.has("provenance/receipt-acceptance.json") {
		load := func(name string) (*envelope.Envelope, error) {
			raw, err := os.ReadFile(filepath.Join(dir, "provenance", name))
			if err != nil {
				return nil, err
			}
			return envelope.ParseFile(raw)
		}
		l, err1 := load("lease.json")
		c, err2 := load("receipt-completion.json")
		a, err3 := load("receipt-acceptance.json")
		if err := errors.Join(err1, err2, err3); err != nil {
			return err
		}
		vr, err := protocol.VerifyReceipt(l, c, a)
		if err != nil {
			return err
		}
		if vr.ReceiptID != m.ReceiptID || vr.Completion.ExecID != m.ExecID || vr.Completion.CalcID != m.CalcID ||
			vr.Lease.RequesterPeerID != m.PublisherPeerID || vr.Acceptance.ParsedResultDigest != m.ParsedResultDigest {
			return errors.New("record: provenance does not match the manifest")
		}
	}
	return nil
}

func (m *Manifest) has(p string) bool {
	for _, f := range m.Files {
		if f.Path == p {
			return true
		}
	}
	return false
}

// Compare compares a reproduction's parsed result with the original (§26.2).
func Compare(origDigest string, orig []byte, reproDigest string, repro []byte, energyTol, forceTol float64, against string) *Comparison {
	c := &Comparison{Against: against, EnergyTolEV: fmtF(energyTol), ForceTolEVA: fmtF(forceTol)}
	if origDigest == reproDigest {
		c.Status = StatusReproducedExact
		c.EnergyDiffEV, c.MaxForceDiffEVA = "0", "0"
		return c
	}
	var a, b qeparse.Result
	if json.Unmarshal(orig, &a) != nil || json.Unmarshal(repro, &b) != nil || a.Energy == nil || b.Energy == nil {
		c.Status = StatusInsufficientMetadata
		return c
	}
	de := math.Abs(a.Energy.Value - b.Energy.Value)
	c.EnergyDiffEV = fmtF(de)
	df := 0.0
	if a.Forces != nil && b.Forces != nil && len(a.Forces.Values) == len(b.Forces.Values) {
		for i := range a.Forces.Values {
			for j := range a.Forces.Values[i] {
				if j < len(b.Forces.Values[i]) {
					df = math.Max(df, math.Abs(a.Forces.Values[i][j]-b.Forces.Values[i][j]))
				}
			}
		}
		c.MaxForceDiffEVA = fmtF(df)
	}
	if a.Converged != b.Converged || de > energyTol || df > forceTol {
		c.Status = StatusDivergent
	} else {
		c.Status = StatusReproducedTolerance
	}
	return c
}

func summarize(cj *job.Canonical, r *qeparse.Result) Summary {
	s := Summary{Calculation: cj.Calculation.Type, Converged: r.Converged, Elements: []string{}}
	if r.Energy != nil {
		s.EnergyEV = fmtF(r.Energy.Value)
	}
	if r.FermiEnergy != nil {
		s.FermiEV = fmtF(r.FermiEnergy.Value)
	} else if r.HighestOccupied != nil {
		s.FermiEV = fmtF(r.HighestOccupied.Value)
	}
	if v, ok := cj.Calculation.Parameters["ecutwfc"].(string); ok {
		s.Ecutwfc = v
	}
	if kp, ok := cj.Calculation.Parameters["kpoints"].([]any); ok && len(kp) >= 3 {
		var parts []string
		for _, k := range kp[:3] {
			parts = append(parts, fmt.Sprint(k))
		}
		s.Kpoints = strings.Join(parts, "x")
	}
	if r.FinalStructure != nil {
		counts := map[string]int{}
		for _, sp := range r.FinalStructure.Species {
			counts[sp]++
		}
		s.NAtoms = int64(len(r.FinalStructure.Species))
		for el := range counts {
			s.Elements = append(s.Elements, el)
		}
		sort.Strings(s.Elements)
		s.Formula = formula(counts, s.Elements)
	}
	return s
}

// formula returns a reduced formula in alphabetical order, e.g. "Si", "NaCl".
func formula(counts map[string]int, els []string) string {
	g := 0
	for _, n := range counts {
		g = gcd(g, n)
	}
	var b strings.Builder
	for _, el := range els {
		b.WriteString(el)
		if n := counts[el] / max(g, 1); n > 1 {
			b.WriteString(strconv.Itoa(n))
		}
	}
	return b.String()
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func fmtF(f float64) string { return strconv.FormatFloat(f, 'g', 10, 64) }

func writeFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func writeChecksums(dir string, files []FileEntry) error {
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "%s  %s\n", strings.TrimPrefix(f.Digest, "sha256:"), f.Path)
	}
	return writeFile(filepath.Join(dir, "checksums.txt"), []byte(b.String()))
}

// CleanPath validates a requested record file path.
func CleanPath(p string) (string, error) {
	if p != path.Clean(p) || !safePath.MatchString(p) {
		return "", fmt.Errorf("record: invalid path %q", p)
	}
	return p, nil
}
