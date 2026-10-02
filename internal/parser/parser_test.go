package parser

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chaeeundad/PFCN/internal/qeparse"
)

func TestRunMatchesNativeParser(t *testing.T) {
	dir := t.TempDir()
	x, _ := os.ReadFile("../qeparse/testdata/data-file-schema.xml")
	out, _ := os.ReadFile("../qeparse/testdata/stdout.txt")
	os.WriteFile(filepath.Join(dir, "data-file-schema.xml"), x, 0o600)
	os.WriteFile(filepath.Join(dir, "stdout.txt"), out, 0o600)

	wasm, ok := Builtin(QEDigest())
	if !ok {
		t.Fatal("embedded parser missing")
	}
	got, err := Run(context.Background(), wasm, dir)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := qeparse.Parse(x, out)
	if !bytes.Equal(got, want) {
		t.Fatalf("WASM parser output differs from native build:\n%s", got)
	}
	if _, ok := Builtin("sha256:0000"); ok {
		t.Fatal("unknown digest must not resolve")
	}
}
