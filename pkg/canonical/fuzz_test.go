package canonical

import (
	"bytes"
	"testing"
)

// Canonicalize must never panic, and its output must be a fixed point.
func FuzzCanonicalize(f *testing.F) {
	for _, s := range []string{`{"b":1,"a":[true,null,"x"]}`, `"\u0000"`, `{"𝄞":1}`, `[]`, `1e3`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		out, err := Canonicalize(in)
		if err != nil {
			return
		}
		again, err := Canonicalize(out)
		if err != nil || !bytes.Equal(out, again) {
			t.Fatalf("not a fixed point: %q -> %q (%v)", out, again, err)
		}
		if err := Check(out); err != nil {
			t.Fatalf("canonical output rejected by Check: %v", err)
		}
	})
}
