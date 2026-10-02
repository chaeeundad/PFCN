package contentid

import "testing"

func TestDeriveIsOrderIndependentAndTyped(t *testing.T) {
	a, err := Derive("pumat.calc.v1", map[string]any{"x": 1, "y": "z"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Derive("pumat.calc.v1", map[string]any{"y": "z", "x": 1})
	if a != b {
		t.Fatal("derive must not depend on map order")
	}
	c, _ := Derive("pumat.exec.v1", map[string]any{"x": 1, "y": "z"})
	if a == c {
		t.Fatal("different types must produce different IDs")
	}
	if _, err := Derive("t", map[string]any{"type": 1}); err == nil {
		t.Fatal("expected error for reserved type key")
	}
}

func TestParse(t *testing.T) {
	d := BLAKE3([]byte("x"))
	if _, _, err := ParseDigest(d); err != nil {
		t.Fatal(err)
	}
	id := Typed(KindCalc, d)
	got, err := ParseTyped(id, KindCalc)
	if err != nil || got != d {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := ParseTyped(id, KindExec); err == nil {
		t.Fatal("expected kind mismatch")
	}
	if _, _, err := ParseDigest("md5:abc"); err == nil {
		t.Fatal("expected bad alg")
	}
}
