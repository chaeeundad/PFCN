package seal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSealOpenAnyOrder(t *testing.T) {
	k, err := NewRecipientKey()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSealer(k.Public(), "pumat:exec:blake3:x")
	if err != nil {
		t.Fatal(err)
	}
	c0 := s.SealChunk(0, []byte("first"))
	c1 := s.SealChunk(1, []byte("second"))

	restored, err := ParseRecipientKey(k.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	o, err := NewOpener(restored, s.Enc, "pumat:exec:blake3:x")
	if err != nil {
		t.Fatal(err)
	}
	p1, err := o.OpenChunk(1, c1)
	if err != nil || string(p1) != "second" {
		t.Fatalf("%q %v", p1, err)
	}
	p0, err := o.OpenChunk(0, c0)
	if err != nil || string(p0) != "first" {
		t.Fatalf("%q %v", p0, err)
	}
	if _, err := o.OpenChunk(1, c0); err == nil {
		t.Fatal("chunk swapped to a different index must fail")
	}
}

func TestWrongExecOrKeyFails(t *testing.T) {
	k, _ := NewRecipientKey()
	other, _ := NewRecipientKey()
	s, _ := NewSealer(k.Public(), "pumat:exec:blake3:a")
	c := s.SealChunk(0, []byte("x"))
	if o, err := NewOpener(k, s.Enc, "pumat:exec:blake3:b"); err == nil {
		if _, err := o.OpenChunk(0, c); err == nil {
			t.Fatal("different exec ID must fail")
		}
	}
	if o, err := NewOpener(other, s.Enc, "pumat:exec:blake3:a"); err == nil {
		if _, err := o.OpenChunk(0, c); err == nil {
			t.Fatal("different key must fail")
		}
	}
}

func TestSealFile(t *testing.T) {
	dir := t.TempDir()
	plain := bytes.Repeat([]byte("abcdefg"), 100)
	pp := filepath.Join(dir, "plain")
	sp := filepath.Join(dir, "sealed")
	os.WriteFile(pp, plain, 0o600)
	k, _ := NewRecipientKey()
	s, _ := NewSealer(k.Public(), "e")
	if err := s.SealFile(pp, sp, 64); err != nil {
		t.Fatal(err)
	}
	sealed, _ := os.ReadFile(sp)
	o, _ := NewOpener(k, s.Enc, "e")
	var got []byte
	step := SealedChunkSize(64)
	for i := 0; i*step < len(sealed); i++ {
		end := min((i+1)*step, len(sealed))
		p, err := o.OpenChunk(uint64(i), sealed[i*step:end])
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p...)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("roundtrip mismatch")
	}
}
