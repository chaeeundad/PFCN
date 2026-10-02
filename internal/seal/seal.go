// Package seal encrypts result bundles to the requester's per-execution key
// (spec §16.6).
//
// Construction: HPKE (RFC 9180) base mode, DHKEM(X25519) + HKDF-SHA256 in
// export-only mode derives a 32-byte key; each chunk is sealed with
// ChaCha20-Poly1305 using the chunk index as nonce and (info, index) as AAD.
// Index-addressed nonces let chunks be fetched and opened in any order, which
// a stateful HPKE context cannot do.
package seal

import (
	"crypto/ecdh"
	"crypto/hpke"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	keyPrefix   = "x25519:"
	exportLabel = "pumat chunk key v1"
)

// Overhead is the per-chunk ciphertext expansion.
const Overhead = chacha20poly1305.Overhead

// RecipientKey is a per-execution X25519 key pair.
type RecipientKey struct {
	priv *ecdh.PrivateKey
}

// NewRecipientKey generates a fresh per-execution key.
func NewRecipientKey() (*RecipientKey, error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &RecipientKey{priv: k}, nil
}

// ParseRecipientKey restores a private key from its raw bytes.
func ParseRecipientKey(raw []byte) (*RecipientKey, error) {
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return nil, err
	}
	return &RecipientKey{priv: k}, nil
}

// Bytes returns the raw private key (store with 0600 permissions only).
func (k *RecipientKey) Bytes() []byte { return k.priv.Bytes() }

// Public returns the public key in protocol form "x25519:<base64url>".
func (k *RecipientKey) Public() string {
	return keyPrefix + base64.RawURLEncoding.EncodeToString(k.priv.PublicKey().Bytes())
}

// ParsePublic parses "x25519:<base64url>".
func ParsePublic(s string) (*ecdh.PublicKey, error) {
	rest, ok := strings.CutPrefix(s, keyPrefix)
	if !ok {
		return nil, fmt.Errorf("seal: invalid recipient key %q", s)
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return nil, fmt.Errorf("seal: invalid recipient key: %w", err)
	}
	return ecdh.X25519().NewPublicKey(raw)
}

func suite() (hpke.KDF, hpke.AEAD) { return hpke.HKDFSHA256(), hpke.ExportOnly() }

// Info binds the sealing context to one execution.
func Info(execID string) []byte { return []byte("pumat-result-v1\x00" + execID) }

// Sealer seals chunks for one recipient.
type Sealer struct {
	aead cipherAEAD
	info []byte
	Enc  []byte // HPKE encapsulated key, sent to the recipient
}

type cipherAEAD interface {
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
}

// NewSealer sets up HPKE to recipientPub and derives the chunk key.
func NewSealer(recipientPub, execID string) (*Sealer, error) {
	pub, err := ParsePublic(recipientPub)
	if err != nil {
		return nil, err
	}
	pk, err := hpke.NewDHKEMPublicKey(pub)
	if err != nil {
		return nil, err
	}
	kdf, aead := suite()
	info := Info(execID)
	enc, s, err := hpke.NewSender(pk, kdf, aead, info)
	if err != nil {
		return nil, err
	}
	key, err := s.Export(exportLabel, chacha20poly1305.KeySize)
	if err != nil {
		return nil, err
	}
	c, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: c, info: info, Enc: enc}, nil
}

// Opener opens chunks sealed to a RecipientKey.
type Opener struct {
	aead cipherAEAD
	info []byte
}

// NewOpener derives the chunk key from enc.
func NewOpener(k *RecipientKey, enc []byte, execID string) (*Opener, error) {
	sk, err := hpke.NewDHKEMPrivateKey(k.priv)
	if err != nil {
		return nil, err
	}
	kdf, aead := suite()
	info := Info(execID)
	r, err := hpke.NewRecipient(enc, sk, kdf, aead, info)
	if err != nil {
		return nil, err
	}
	key, err := r.Export(exportLabel, chacha20poly1305.KeySize)
	if err != nil {
		return nil, err
	}
	c, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	return &Opener{aead: c, info: info}, nil
}

func nonceAAD(info []byte, index uint64) ([]byte, []byte) {
	nonce := make([]byte, chacha20poly1305.NonceSize)
	binary.BigEndian.PutUint64(nonce[4:], index)
	aad := make([]byte, 0, len(info)+8)
	aad = append(aad, info...)
	aad = binary.BigEndian.AppendUint64(aad, index)
	return nonce, aad
}

// SealChunk encrypts chunk number index.
func (s *Sealer) SealChunk(index uint64, plain []byte) []byte {
	nonce, aad := nonceAAD(s.info, index)
	return s.aead.Seal(nil, nonce, plain, aad)
}

// OpenChunk decrypts chunk number index.
func (o *Opener) OpenChunk(index uint64, sealed []byte) ([]byte, error) {
	nonce, aad := nonceAAD(o.info, index)
	p, err := o.aead.Open(nil, nonce, sealed, aad)
	if err != nil {
		return nil, fmt.Errorf("seal: chunk %d: %w", index, err)
	}
	return p, nil
}

// SealFile reads plainPath in chunkSize chunks and writes sealed chunks to
// sealedPath. Sealed chunk i occupies [i*(chunkSize+Overhead), ...).
func (s *Sealer) SealFile(plainPath, sealedPath string, chunkSize int) error {
	in, err := os.Open(plainPath)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(sealedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	buf := make([]byte, chunkSize)
	for i := uint64(0); ; i++ {
		n, rerr := io.ReadFull(in, buf)
		if n > 0 {
			if _, err := out.Write(s.SealChunk(i, buf[:n])); err != nil {
				out.Close()
				return err
			}
		}
		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr
		}
	}
	return out.Close()
}

// SealedChunkSize is the on-disk size of a full sealed chunk.
func SealedChunkSize(chunkSize int) int { return chunkSize + Overhead }
