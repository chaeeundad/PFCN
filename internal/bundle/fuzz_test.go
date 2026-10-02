package bundle

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Extraction of hostile tars must stay inside dest.
func FuzzExtract(f *testing.F) {
	good, _ := Build([]File{{"a/b.txt", []byte("x")}})
	f.Add(good)
	f.Fuzz(func(t *testing.T, in []byte) {
		dest := t.TempDir()
		names, err := Extract(bytes.NewReader(in), dest, Limits{MaxFiles: 50, MaxTotalBytes: 1 << 20})
		if err != nil {
			return
		}
		for _, n := range names {
			p := filepath.Join(dest, filepath.FromSlash(n))
			if !strings.HasPrefix(p, dest+string(filepath.Separator)) {
				t.Fatalf("escaped dest: %s", n)
			}
			if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
				t.Fatalf("non-regular file extracted: %s", n)
			}
		}
	})
}
