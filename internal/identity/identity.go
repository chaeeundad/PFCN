// Package identity manages the node's Ed25519 identity (spec §8.1).
//
// The private key never leaves the node. Key backup/export is intentionally
// not implemented (Epic B).
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

const keyFileName = "node.key"

// Identity is a loaded node identity.
type Identity struct {
	Private ed25519.PrivateKey
	PrivKey crypto.PrivKey
	PeerID  peer.ID
}

// KeyPath returns the identity key path inside dir.
func KeyPath(dir string) string { return filepath.Join(dir, keyFileName) }

// Exists reports whether an identity key exists in dir.
func Exists(dir string) bool {
	_, err := os.Stat(KeyPath(dir))
	return err == nil
}

// Generate creates a new identity in dir. It refuses to overwrite.
func Generate(dir string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	data := base64.StdEncoding.EncodeToString(priv.Seed()) + "\n"
	f, err := os.OpenFile(KeyPath(dir), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("identity: create key: %w", err)
	}
	if _, err := f.WriteString(data); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return FromSeed(priv.Seed())
}

// Load reads the identity from dir. It fails closed on loose permissions or a
// corrupted key.
func Load(dir string) (*Identity, error) {
	return LoadKeyFile(KeyPath(dir))
}

// LoadKeyFile reads a base64 Ed25519 seed file with strict permission checks.
// It is also used for solver signing keys.
func LoadKeyFile(path string) (*Identity, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("identity: %s has permissions %o; must not be group/other accessible", path, st.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("identity: key file is corrupted")
	}
	return FromSeed(seed)
}

// WriteKeyFile writes a new random Ed25519 seed to path (mode 0600, no overwrite).
func WriteKeyFile(path string) (*Identity, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return FromSeed(priv.Seed())
}

// FromSeed builds an identity from a 32-byte Ed25519 seed.
func FromSeed(seed []byte) (*Identity, error) {
	priv := ed25519.NewKeyFromSeed(seed)
	pk, err := crypto.UnmarshalEd25519PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	id, err := peer.IDFromPrivateKey(pk)
	if err != nil {
		return nil, err
	}
	return &Identity{Private: priv, PrivKey: pk, PeerID: id}, nil
}

// Sign signs msg with the identity key.
func (i *Identity) Sign(msg []byte) []byte { return ed25519.Sign(i.Private, msg) }

// PublicKeyFromPeerID extracts the Ed25519 public key embedded in a peer ID.
func PublicKeyFromPeerID(s string) (ed25519.PublicKey, error) {
	id, err := peer.Decode(s)
	if err != nil {
		return nil, fmt.Errorf("identity: invalid peer ID %q: %w", s, err)
	}
	pk, err := id.ExtractPublicKey()
	if err != nil {
		return nil, fmt.Errorf("identity: peer ID %q has no embedded key: %w", s, err)
	}
	if pk.Type() != crypto.Ed25519 {
		return nil, fmt.Errorf("identity: peer ID %q is not Ed25519", s)
	}
	raw, err := pk.Raw()
	if err != nil {
		return nil, err
	}
	return ed25519.PublicKey(raw), nil
}
