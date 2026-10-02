// Package contentid implements Pumat digests and typed identifiers
// (spec §7.5, §12.4, §12.5).
package contentid

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"lukechampine.com/blake3"

	"github.com/chaeeundad/PFCN/pkg/canonical"
)

const (
	AlgBLAKE3 = "blake3"
	AlgSHA256 = "sha256"
)

// Typed identifier kinds (§12.5).
const (
	KindCalc    = "calc"
	KindExec    = "exec"
	KindLease   = "lease"
	KindReceipt = "receipt"
	KindRecord  = "record"
)

// BLAKE3 returns "blake3:<hex>" of data.
func BLAKE3(data []byte) string {
	sum := blake3.Sum256(data)
	return AlgBLAKE3 + ":" + hex.EncodeToString(sum[:])
}

// SHA256 returns "sha256:<hex>" of data.
func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return AlgSHA256 + ":" + hex.EncodeToString(sum[:])
}

// SHA256Reader streams r and returns "sha256:<hex>" and the byte count.
func SHA256Reader(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return AlgSHA256 + ":" + hex.EncodeToString(h.Sum(nil)), n, nil
}

// Derive computes blake3(JCS({"type": typ, ...fields})) as "blake3:<hex>".
// fields must not contain a "type" key.
func Derive(typ string, fields map[string]any) (string, error) {
	if _, ok := fields["type"]; ok {
		return "", fmt.Errorf("contentid: fields must not contain \"type\"")
	}
	obj := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		obj[k] = v
	}
	obj["type"] = typ
	b, err := canonical.Marshal(obj)
	if err != nil {
		return "", err
	}
	return BLAKE3(b), nil
}

// Typed returns "pumat:<kind>:<digest>".
func Typed(kind, digest string) string {
	return "pumat:" + kind + ":" + digest
}

// ParseDigest validates "<alg>:<64 lowercase hex>".
func ParseDigest(s string) (alg string, sum []byte, err error) {
	alg, h, ok := strings.Cut(s, ":")
	if !ok || (alg != AlgBLAKE3 && alg != AlgSHA256) {
		return "", nil, fmt.Errorf("contentid: invalid digest %q", s)
	}
	if len(h) != 64 || strings.ToLower(h) != h {
		return "", nil, fmt.Errorf("contentid: invalid digest hex in %q", s)
	}
	sum, err = hex.DecodeString(h)
	if err != nil {
		return "", nil, fmt.Errorf("contentid: invalid digest hex in %q", s)
	}
	return alg, sum, nil
}

// ParseTyped validates "pumat:<kind>:<digest>" and returns the digest part.
func ParseTyped(s, wantKind string) (string, error) {
	rest, ok := strings.CutPrefix(s, "pumat:"+wantKind+":")
	if !ok {
		return "", fmt.Errorf("contentid: expected pumat:%s:..., got %q", wantKind, s)
	}
	if _, _, err := ParseDigest(rest); err != nil {
		return "", err
	}
	return rest, nil
}

// Short returns a compact display form of a typed ID or digest.
func Short(s string) string {
	i := strings.LastIndex(s, ":")
	if i < 0 || len(s)-i-1 < 12 {
		return s
	}
	return s[:i+1] + s[i+1:i+13]
}
