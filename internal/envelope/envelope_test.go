package envelope

import (
	"testing"

	"github.com/chaeeundad/PFCN/internal/identity"
)

type obj struct {
	Schema string `json:"schema"`
	Value  int64  `json:"value"`
}

func newID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSignVerifyAndTamper(t *testing.T) {
	a, b := newID(t), newID(t)
	e, err := Sign(a, "pumat.test.v1", obj{Schema: "pumat.test.v1", Value: 7})
	if err != nil {
		t.Fatal(err)
	}
	e.AddSignature(b)
	if err := e.VerifySigners("pumat.test.v1", a.PeerID.String(), b.PeerID.String()); err != nil {
		t.Fatal(err)
	}

	raw, _ := e.MarshalFile()
	back, err := ParseFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := back.Verify("pumat.test.v1"); err != nil {
		t.Fatal(err)
	}

	tampered := *e
	tampered.Payload = []byte(`{"schema":"pumat.test.v1","value":8}`)
	if err := tampered.Verify("pumat.test.v1"); err == nil {
		t.Fatal("tampered payload must fail")
	}
	if err := e.Verify("pumat.other.v1"); err == nil {
		t.Fatal("schema mismatch must fail")
	}
	if err := e.VerifySigners("pumat.test.v1", a.PeerID.String()); err == nil {
		t.Fatal("signer set mismatch must fail")
	}
}

func TestSchemaFieldMustMatch(t *testing.T) {
	if _, err := New("pumat.test.v1", obj{Schema: "pumat.wrong.v1"}); err == nil {
		t.Fatal("expected schema mismatch")
	}
}

func TestNonCanonicalPayloadRejected(t *testing.T) {
	a := newID(t)
	e := &Envelope{Schema: "pumat.test.v1", Payload: []byte(`{"value":1, "schema":"pumat.test.v1"}`)}
	e.AddSignature(a)
	if err := e.Verify("pumat.test.v1"); err == nil {
		t.Fatal("non-canonical payload must be rejected even if signed")
	}
}
