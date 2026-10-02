// Package protocol defines the signed Pumat protocol objects and their wire
// conversion (spec §11, §15, §20).
package protocol

import (
	"errors"
	"fmt"
	"time"

	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/pb"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

// Schemas.
const (
	SchemaCapability   = "pumat.capability.v1"
	SchemaLeaseRequest = "pumat.lease.request.v1"
	SchemaLease        = "pumat.lease.v1"
	SchemaCompletion   = "pumat.receipt.completion.v1"
	SchemaAcceptance   = "pumat.receipt.acceptance.v1"
	SchemaEvent        = "pumat.event.v1"
	SchemaCancel       = "pumat.cancel.v1"
	execIDSchema       = "pumat.exec.v1"
)

// Protocol IDs.
const (
	ProtoCapability = "/pumat/capability/1.0.0"
	ProtoLease      = "/pumat/lease/1.0.0"
	ProtoInput      = "/pumat/input/1.0.0"
	ProtoResult     = "/pumat/result/1.0.0"
)

// ClockSkew is the tolerance for issued_at and deadlines (§15.5).
const ClockSkew = 120 * time.Second

// CapabilityTTL is the capability document lifetime (§11.3).
const CapabilityTTL = 180 * time.Second

// Resources is a resource allocation in base units.
type Resources struct {
	CPUMillicores int64 `json:"cpu_millicores"`
	MemoryBytes   int64 `json:"memory_bytes"`
	GPUCount      int64 `json:"gpu_count"`
	DiskBytes     int64 `json:"disk_bytes"`
}

// Capability is the signed capability document (§11.2).
type Capability struct {
	Schema      string             `json:"schema"`
	PeerID      string             `json:"peer_id"`
	Namespace   string             `json:"namespace"`
	Sequence    int64              `json:"sequence"`
	IssuedAt    string             `json:"issued_at"`
	ExpiresAt   string             `json:"expires_at"`
	Status      string             `json:"status"` // available | paused | draining | busy
	Platform    string             `json:"platform"`
	CPU         CapabilityCPU      `json:"cpu"`
	MemoryBytes int64              `json:"memory_bytes"`
	Runtime     CapabilityRuntime  `json:"runtime"`
	Solvers     []CapabilitySolver `json:"solvers"`
	Policy      CapabilityPolicy   `json:"policy"`
}

type CapabilityCPU struct {
	Model          string `json:"model"`
	AvailableCores int64  `json:"available_cores"`
}

type CapabilityRuntime struct {
	Engine              string `json:"engine"`
	Container           bool   `json:"container"`
	MicroVM             bool   `json:"microvm"`
	WorkspaceEncryption bool   `json:"workspace_encryption"`
}

type CapabilitySolver struct {
	Name           string `json:"name"`
	ArtifactCached bool   `json:"artifact_cached"`
}

type CapabilityPolicy struct {
	MaxWalltimeSeconds        int64    `json:"max_walltime_seconds"`
	MaxResultRetentionSeconds int64    `json:"max_result_retention_seconds"`
	MaxInputBytes             int64    `json:"max_input_bytes"`
	Network                   string   `json:"network"`
	Visibility                []string `json:"visibility"`
}

// LeaseRequest is sent by the requester (§14.4).
type LeaseRequest struct {
	Schema                 string    `json:"schema"`
	Namespace              string    `json:"namespace"`
	ExecID                 string    `json:"exec_id"`
	CalcID                 string    `json:"calc_id"`
	RequesterPeerID        string    `json:"requester_peer_id"`
	WorkerPeerID           string    `json:"worker_peer_id"`
	RequesterNonce         string    `json:"requester_nonce"`
	SolverManifestDigest   string    `json:"solver_manifest_digest"`
	Entrypoint             string    `json:"entrypoint"`
	Visibility             string    `json:"visibility"`
	Resources              Resources `json:"resources"`
	MaxWalltimeSeconds     int64     `json:"max_walltime_seconds"`
	InputBundleDigest      string    `json:"input_bundle_digest"`
	InputBundleSize        int64     `json:"input_bundle_size"`
	ResultRetentionSeconds int64     `json:"result_retention_seconds"`
	ResultRecipientKey     string    `json:"result_recipient_key"`
	CustodianPeerID        string    `json:"custodian_peer_id"`
	IssuedAt               string    `json:"issued_at"`
}

// Lease is the bilateral lease (§15.3).
type Lease struct {
	Schema                 string    `json:"schema"`
	Namespace              string    `json:"namespace"`
	ExecID                 string    `json:"exec_id"`
	CalcID                 string    `json:"calc_id"`
	RequesterPeerID        string    `json:"requester_peer_id"`
	WorkerPeerID           string    `json:"worker_peer_id"`
	RequesterNonce         string    `json:"requester_nonce"`
	WorkerNonce            string    `json:"worker_nonce"`
	SolverManifestDigest   string    `json:"solver_manifest_digest"`
	ArtifactDigest         string    `json:"artifact_digest"`
	Platform               string    `json:"platform"`
	Entrypoint             string    `json:"entrypoint"`
	Visibility             string    `json:"visibility"`
	Resources              Resources `json:"resources"`
	InputBundleDigest      string    `json:"input_bundle_digest"`
	InputBundleSize        int64     `json:"input_bundle_size"`
	MaxOutputBytes         int64     `json:"max_output_bytes"`
	IssuedAt               string    `json:"issued_at"`
	InputDeadline          string    `json:"input_deadline"`
	MaxWalltimeSeconds     int64     `json:"max_walltime_seconds"`
	ResultRetentionSeconds int64     `json:"result_retention_seconds"`
	ResultRecipientKey     string    `json:"result_recipient_key"`
	CustodianPeerID        string    `json:"custodian_peer_id"`
}

// ResourceClaim is the worker's resource usage claim (§20.2).
type ResourceClaim struct {
	CPUMillicores int64 `json:"cpu_millicores"`
	GPUCount      int64 `json:"gpu_count"`
	WallSeconds   int64 `json:"wall_seconds"`
}

// Outcomes of a completion.
const (
	OutcomeSucceeded        = "succeeded"
	OutcomeSolverFailed     = "solver_failed"
	OutcomeWalltimeExceeded = "walltime_exceeded"
	OutcomeResourceExceeded = "resource_exceeded"
)

// Completion is the worker completion statement (§20.2).
type Completion struct {
	Schema               string        `json:"schema"`
	ExecID               string        `json:"exec_id"`
	CalcID               string        `json:"calc_id"`
	LeaseID              string        `json:"lease_id"`
	RequesterPeerID      string        `json:"requester_peer_id"`
	WorkerPeerID         string        `json:"worker_peer_id"`
	SolverManifestDigest string        `json:"solver_manifest_digest"`
	ArtifactDigest       string        `json:"artifact_digest"`
	Platform             string        `json:"platform"`
	InputBundleDigest    string        `json:"input_bundle_digest"`
	OutputBundleDigest   string        `json:"output_bundle_digest"`
	SealedBundleDigest   string        `json:"sealed_bundle_digest"`
	Outcome              string        `json:"outcome"`
	ExitCode             int64         `json:"exit_code"`
	ResourceClaim        ResourceClaim `json:"resource_claim"`
	StartedAt            string        `json:"started_at"`
	FinishedAt           string        `json:"finished_at"`
}

// Cancel is a requester-signed cancellation of an execution before SEALED.
type Cancel struct {
	Schema   string `json:"schema"`
	ExecID   string `json:"exec_id"`
	IssuedAt string `json:"issued_at"`
}

// Verdicts of an acceptance.
const (
	VerdictAccepted        = "accepted"
	VerdictOutputMismatch  = "output_mismatch"
	VerdictMalformedOutput = "malformed_output"
)

// Acceptance is the requester acceptance statement (§20.2).
type Acceptance struct {
	Schema             string `json:"schema"`
	ReceiptID          string `json:"receipt_id"`
	ExecID             string `json:"exec_id"`
	OutputBundleDigest string `json:"output_bundle_digest"`
	ParserDigest       string `json:"parser_digest"`
	ParsedResultDigest string `json:"parsed_result_digest"`
	AcceptedAt         string `json:"accepted_at"`
	Verdict            string `json:"verdict"`
}

// ExecID derives the execution ID (§12.4).
func ExecID(calcID, requester, worker, nonce string) (string, error) {
	d, err := contentid.Derive(execIDSchema, map[string]any{
		"calc_id": calcID, "requester_peer_id": requester,
		"worker_peer_id": worker, "requester_nonce": nonce,
	})
	if err != nil {
		return "", err
	}
	return contentid.Typed(contentid.KindExec, d), nil
}

// LeaseID is blake3 of the lease payload (§12.4).
func LeaseID(env *envelope.Envelope) string {
	return contentid.Typed(contentid.KindLease, env.Digest())
}

// ReceiptID is blake3 of the completion payload (§12.5).
func ReceiptID(env *envelope.Envelope) string {
	return contentid.Typed(contentid.KindReceipt, env.Digest())
}

// Now formats t as an RFC 3339 UTC second-precision timestamp.
func Now(t time.Time) string { return t.UTC().Truncate(time.Second).Format(time.RFC3339) }

// ParseTime parses a protocol timestamp.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("protocol: invalid timestamp %q", s)
	}
	return t, nil
}

// CheckIssued rejects timestamps too far in the future (§15.5).
func CheckIssued(s string, now time.Time) error {
	t, err := ParseTime(s)
	if err != nil {
		return err
	}
	if t.After(now.Add(ClockSkew)) {
		return errors.New("protocol: issued_at is in the future beyond clock-skew tolerance")
	}
	return nil
}

// ToPB converts an envelope to its wire form.
func ToPB(e *envelope.Envelope) *pb.SignedEnvelope {
	if e == nil {
		return nil
	}
	out := &pb.SignedEnvelope{Schema: e.Schema, Payload: e.Payload}
	for _, s := range e.Signatures {
		out.Signatures = append(out.Signatures, &pb.Signature{Signer: s.Signer, Alg: s.Alg, Sig: s.Sig})
	}
	return out
}

// FromPB converts a wire envelope.
func FromPB(p *pb.SignedEnvelope) (*envelope.Envelope, error) {
	if p == nil {
		return nil, errors.New("protocol: missing envelope")
	}
	e := &envelope.Envelope{Schema: p.Schema, Payload: p.Payload}
	for _, s := range p.Signatures {
		e.Signatures = append(e.Signatures, envelope.Signature{Signer: s.Signer, Alg: s.Alg, Sig: s.Sig})
	}
	return e, nil
}
