package solver

import (
	"testing"
	"time"

	"github.com/chaeeundad/PFCN/internal/identity"
)

func TestRevocations(t *testing.T) {
	key, _ := identity.Generate(t.TempDir())
	m, _ := FromYAML([]byte(manifestYAML), time.Now())
	env, _ := Sign(m, key)
	pol := Policy{TrustedSigners: []string{key.PeerID.String()}}
	v, err := pol.Verify(env)
	if err != nil {
		t.Fatal(err)
	}
	next := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	for _, e := range []Revoked{
		{Kind: RevokeManifest, Digest: v.Digest, Reason: "broken_scientific_output"},
		{Kind: RevokeArtifact, Digest: m.Artifacts[0].Digest, Reason: "malicious_artifact"},
		{Kind: RevokeSigner, Identity: key.PeerID.String(), Reason: "compromised_signing_identity"},
	} {
		rl := &RevocationList{Schema: RevocationSchema, Namespace: "/pumat/public/v1", Sequence: 1, IssuedAt: next, NextUpdateBefore: next, Revoked: []Revoked{e}}
		renv, err := SignRevocations(rl, key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pol.VerifyRevocations(renv)
		if err != nil {
			t.Fatal(err)
		}
		if err := got.Check(v); err == nil {
			t.Errorf("%s revocation not enforced", e.Kind)
		}
	}
	empty := &RevocationList{Schema: RevocationSchema, Sequence: 2, NextUpdateBefore: next, Revoked: []Revoked{}}
	if err := empty.Check(v); err != nil {
		t.Fatal(err)
	}
	if empty.Stale(time.Now(), 0) || !empty.Stale(time.Now().Add(72*time.Hour), time.Hour) {
		t.Fatal("staleness")
	}
	bad := &RevocationList{Schema: RevocationSchema, Sequence: 1, NextUpdateBefore: next, Revoked: []Revoked{{Kind: RevokeManifest, Digest: v.Digest, Reason: "because"}}}
	if _, err := SignRevocations(bad, key); err == nil {
		t.Fatal("invalid reason must be rejected")
	}
}
