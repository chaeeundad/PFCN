// Package envelope implements the Pumat signed envelope (spec §7.4.1).
//
// A signed object is carried as exact canonical bytes and is never
// re-serialized before verification.
package envelope

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/chaeeundad/PFCN/internal/identity"
	"github.com/chaeeundad/PFCN/pkg/canonical"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

const (
	sigDomain  = "pumat-sig-v1"
	AlgEd25519 = "ed25519"
)

// Signature is one signature over the envelope payload.
type Signature struct {
	Signer string `json:"signer"` // peer ID; the Ed25519 public key is embedded in it
	Alg    string `json:"alg"`
	Sig    []byte `json:"sig"`
}

// Envelope is a signed object. Its JSON form is used for files and storage;
// the wire form is the protobuf SignedEnvelope with the same fields.
type Envelope struct {
	Schema     string      `json:"schema"`
	Payload    []byte      `json:"payload"`
	Signatures []Signature `json:"signatures"`
}

// New creates an unsigned envelope from v, canonicalized. v must carry a
// "schema" field equal to schema.
func New(schema string, v any) (*Envelope, error) {
	payload, err := canonical.Marshal(v)
	if err != nil {
		return nil, err
	}
	if err := checkSchemaField(schema, payload); err != nil {
		return nil, err
	}
	return &Envelope{Schema: schema, Payload: payload}, nil
}

// Sign creates and signs an envelope in one step.
func Sign(id *identity.Identity, schema string, v any) (*Envelope, error) {
	e, err := New(schema, v)
	if err != nil {
		return nil, err
	}
	e.AddSignature(id)
	return e, nil
}

// AddSignature appends id's signature over the payload.
func (e *Envelope) AddSignature(id *identity.Identity) {
	e.Signatures = append(e.Signatures, Signature{
		Signer: id.PeerID.String(),
		Alg:    AlgEd25519,
		Sig:    id.Sign(signingInput(e.Schema, e.Payload)),
	})
}

// Verify checks canonical form, schema consistency, and every signature.
// At least one signature is required.
func (e *Envelope) Verify(wantSchema string) error {
	if e.Schema != wantSchema {
		return fmt.Errorf("envelope: schema %q, want %q", e.Schema, wantSchema)
	}
	if err := canonical.Check(e.Payload); err != nil {
		return fmt.Errorf("envelope: %w", err)
	}
	if err := checkSchemaField(e.Schema, e.Payload); err != nil {
		return err
	}
	if len(e.Signatures) == 0 {
		return errors.New("envelope: no signatures")
	}
	in := signingInput(e.Schema, e.Payload)
	seen := map[string]bool{}
	for _, s := range e.Signatures {
		if s.Alg != AlgEd25519 {
			return fmt.Errorf("envelope: unsupported signature alg %q", s.Alg)
		}
		if seen[s.Signer] {
			return fmt.Errorf("envelope: duplicate signature from %s", s.Signer)
		}
		seen[s.Signer] = true
		pub, err := identity.PublicKeyFromPeerID(s.Signer)
		if err != nil {
			return err
		}
		if !ed25519.Verify(pub, in, s.Sig) {
			return fmt.Errorf("envelope: invalid signature from %s", s.Signer)
		}
	}
	return nil
}

// VerifySigners verifies the envelope and requires exactly the given signers.
func (e *Envelope) VerifySigners(wantSchema string, signers ...string) error {
	if err := e.Verify(wantSchema); err != nil {
		return err
	}
	if len(e.Signatures) != len(signers) {
		return fmt.Errorf("envelope: %d signatures, want %d", len(e.Signatures), len(signers))
	}
	for _, s := range signers {
		if !e.SignedBy(s) {
			return fmt.Errorf("envelope: missing signature from %s", s)
		}
	}
	return nil
}

// SignedBy reports whether the envelope carries a signature from signer.
// It does not verify; call Verify first.
func (e *Envelope) SignedBy(signer string) bool {
	for _, s := range e.Signatures {
		if s.Signer == signer {
			return true
		}
	}
	return false
}

// Decode decodes the payload into v (strict: canonical, no unknown fields).
func (e *Envelope) Decode(v any) error { return canonical.Unmarshal(e.Payload, v) }

// Digest returns blake3 of the payload bytes.
func (e *Envelope) Digest() string { return contentid.BLAKE3(e.Payload) }

// SHA256 returns sha256 of the payload bytes (used for solver manifests, §7.7).
func (e *Envelope) SHA256() string { return contentid.SHA256(e.Payload) }

// SamePayload reports whether two envelopes carry identical payload bytes.
func (e *Envelope) SamePayload(o *Envelope) bool {
	return e.Schema == o.Schema && bytes.Equal(e.Payload, o.Payload)
}

// MarshalFile encodes the envelope as indented JSON for files.
func (e *Envelope) MarshalFile() ([]byte, error) { return json.MarshalIndent(e, "", "  ") }

// ParseFile decodes an envelope from its JSON file form.
func ParseFile(b []byte) (*Envelope, error) {
	var e Envelope
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("envelope: %w", err)
	}
	return &e, nil
}

func signingInput(schema string, payload []byte) []byte {
	b := make([]byte, 0, len(sigDomain)+len(schema)+len(payload)+2)
	b = append(b, sigDomain...)
	b = append(b, 0)
	b = append(b, schema...)
	b = append(b, 0)
	return append(b, payload...)
}

func checkSchemaField(schema string, payload []byte) error {
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(payload, &head); err != nil {
		return fmt.Errorf("envelope: %w", err)
	}
	if head.Schema != schema {
		return fmt.Errorf("envelope: payload schema %q does not match envelope schema %q", head.Schema, schema)
	}
	return nil
}
