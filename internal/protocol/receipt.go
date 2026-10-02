package protocol

import (
	"errors"
	"fmt"

	"github.com/chaeeundad/PFCN/internal/envelope"
)

// VerifiedReceipt is the result of checking a receipt bundle.
type VerifiedReceipt struct {
	Lease      Lease
	Completion Completion
	Acceptance *Acceptance // nil if unacknowledged
	ReceiptID  string
	State      string // completed | disputed | unacknowledged
}

// VerifyReceipt checks the full chain lease -> completion -> acceptance
// (§20.2, §20.3). Any modified field breaks a signature or a binding.
func VerifyReceipt(lease, completion, acceptance *envelope.Envelope) (*VerifiedReceipt, error) {
	if lease == nil || completion == nil {
		return nil, errors.New("receipt: lease and completion are required")
	}
	var r VerifiedReceipt
	if err := lease.Decode(&r.Lease); err != nil {
		return nil, fmt.Errorf("receipt: lease: %w", err)
	}
	if err := lease.VerifySigners(SchemaLease, r.Lease.RequesterPeerID, r.Lease.WorkerPeerID); err != nil {
		return nil, fmt.Errorf("receipt: lease: %w", err)
	}
	if err := completion.VerifySigners(SchemaCompletion, r.Lease.WorkerPeerID); err != nil {
		return nil, fmt.Errorf("receipt: completion: %w", err)
	}
	if err := completion.Decode(&r.Completion); err != nil {
		return nil, fmt.Errorf("receipt: completion: %w", err)
	}
	c, l := r.Completion, r.Lease
	switch {
	case c.LeaseID != LeaseID(lease):
		return nil, errors.New("receipt: completion references a different lease")
	case c.ExecID != l.ExecID || c.CalcID != l.CalcID:
		return nil, errors.New("receipt: completion IDs do not match the lease")
	case c.RequesterPeerID != l.RequesterPeerID || c.WorkerPeerID != l.WorkerPeerID:
		return nil, errors.New("receipt: completion peers do not match the lease")
	case c.InputBundleDigest != l.InputBundleDigest || c.ArtifactDigest != l.ArtifactDigest || c.SolverManifestDigest != l.SolverManifestDigest:
		return nil, errors.New("receipt: completion input/solver does not match the lease")
	}
	r.ReceiptID = ReceiptID(completion)
	r.State = "unacknowledged"
	if acceptance == nil {
		return &r, nil
	}
	if err := acceptance.VerifySigners(SchemaAcceptance, l.RequesterPeerID); err != nil {
		return nil, fmt.Errorf("receipt: acceptance: %w", err)
	}
	var a Acceptance
	if err := acceptance.Decode(&a); err != nil {
		return nil, fmt.Errorf("receipt: acceptance: %w", err)
	}
	if a.ReceiptID != r.ReceiptID || a.ExecID != c.ExecID || a.OutputBundleDigest != c.OutputBundleDigest {
		return nil, errors.New("receipt: acceptance does not reference this completion")
	}
	r.Acceptance = &a
	if a.Verdict == VerdictAccepted {
		r.State = "completed"
	} else {
		r.State = "disputed"
	}
	return &r, nil
}
