package solver

import (
	"errors"
	"fmt"
	"time"

	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/identity"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

const RevocationSchema = "pumat.revocation.v1"

// Revocation kinds and reasons (§13.5).
const (
	RevokeManifest = "solver_manifest"
	RevokeArtifact = "artifact"
	RevokeSigner   = "signer"
)

var validReasons = map[string]bool{
	"critical_vulnerability": true, "malicious_artifact": true,
	"broken_scientific_output": true, "compromised_signing_identity": true,
}

// RevocationList is a signed list of revoked manifests, artifacts and signers.
type RevocationList struct {
	Schema           string    `json:"schema"`
	Namespace        string    `json:"namespace"`
	Sequence         int64     `json:"sequence"`
	IssuedAt         string    `json:"issued_at"`
	NextUpdateBefore string    `json:"next_update_before"`
	Revoked          []Revoked `json:"revoked"`
}

// Revoked is one entry. Digest is set for manifests/artifacts, Identity for signers.
type Revoked struct {
	Kind     string `json:"kind"`
	Digest   string `json:"digest"`
	Identity string `json:"identity"`
	Reason   string `json:"reason"`
}

func (r *RevocationList) validate() error {
	if r.Schema != RevocationSchema || r.Sequence < 1 {
		return errors.New("revocation: invalid schema or sequence")
	}
	if _, err := time.Parse(time.RFC3339, r.NextUpdateBefore); err != nil {
		return errors.New("revocation: invalid next_update_before")
	}
	for _, e := range r.Revoked {
		if !validReasons[e.Reason] {
			return fmt.Errorf("revocation: invalid reason %q", e.Reason)
		}
		switch e.Kind {
		case RevokeManifest, RevokeArtifact:
			if _, _, err := contentid.ParseDigest(e.Digest); err != nil {
				return err
			}
		case RevokeSigner:
			if _, err := identity.PublicKeyFromPeerID(e.Identity); err != nil {
				return err
			}
		default:
			return fmt.Errorf("revocation: invalid kind %q", e.Kind)
		}
	}
	return nil
}

// SignRevocations validates and signs a list.
func SignRevocations(r *RevocationList, key *identity.Identity) (*envelope.Envelope, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	return envelope.Sign(key, RevocationSchema, r)
}

// VerifyRevocations checks a list against the trusted signers. A list signed
// by a revoked signer is still accepted if another trusted signer signed it.
func (p Policy) VerifyRevocations(env *envelope.Envelope) (*RevocationList, error) {
	if err := env.Verify(RevocationSchema); err != nil {
		return nil, err
	}
	trusted := false
	for _, s := range p.TrustedSigners {
		trusted = trusted || env.SignedBy(s)
	}
	if !trusted {
		return nil, errors.New("revocation: list is not signed by a trusted solver signer")
	}
	var r RevocationList
	if err := env.Decode(&r); err != nil {
		return nil, err
	}
	return &r, r.validate()
}

// Check returns an error if the manifest, any of its artifacts, or one of its
// signers is revoked.
func (r *RevocationList) Check(v *Verified) error {
	if r == nil {
		return nil
	}
	for _, e := range r.Revoked {
		switch e.Kind {
		case RevokeManifest:
			if e.Digest == v.Digest {
				return fmt.Errorf("solver manifest %s is revoked (%s)", v.Digest, e.Reason)
			}
		case RevokeArtifact:
			for _, a := range v.Manifest.Artifacts {
				if a.Digest == e.Digest {
					return fmt.Errorf("solver artifact %s is revoked (%s)", e.Digest, e.Reason)
				}
			}
		case RevokeSigner:
			if v.Envelope != nil && v.Envelope.SignedBy(e.Identity) && len(v.Envelope.Signatures) == 1 {
				return fmt.Errorf("solver signer %s is revoked (%s)", e.Identity, e.Reason)
			}
		}
	}
	return nil
}

// Stale reports whether the list is past next_update_before plus grace.
func (r *RevocationList) Stale(now time.Time, grace time.Duration) bool {
	if r == nil {
		return true
	}
	t, err := time.Parse(time.RFC3339, r.NextUpdateBefore)
	return err != nil || now.After(t.Add(grace))
}
