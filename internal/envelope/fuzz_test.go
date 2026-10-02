package envelope

import "testing"

func FuzzParseAndVerify(f *testing.F) {
	f.Add([]byte(`{"schema":"pumat.test.v1","payload":"eyJzY2hlbWEiOiJwdW1hdC50ZXN0LnYxIn0=","signatures":[]}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		e, err := ParseFile(in)
		if err != nil {
			return
		}
		_ = e.Verify("pumat.test.v1") // must not panic
	})
}
