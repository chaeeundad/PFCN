package identity

import (
	"crypto/ed25519"
	"os"
	"testing"
)

func TestGenerateLoadStable(t *testing.T) {
	dir := t.TempDir()
	a, err := Generate(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.PeerID != b.PeerID {
		t.Fatal("identity must be stable across reload")
	}
	if _, err := Generate(dir); err == nil {
		t.Fatal("must refuse to overwrite identity")
	}
	pub, err := PublicKeyFromPeerID(a.PeerID.String())
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("hello")
	if !ed25519.Verify(pub, msg, a.Sign(msg)) {
		t.Fatal("signature must verify with key from peer ID")
	}
}

func TestLoadFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if _, err := Generate(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(KeyPath(dir), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected permission error")
	}
	if err := os.Chmod(KeyPath(dir), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(KeyPath(dir), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected corruption error")
	}
}
