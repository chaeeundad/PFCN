package bundle

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildDeterministic(t *testing.T) {
	a, err := Build([]File{{"b.txt", []byte("2")}, {"dir/a.txt", []byte("1")}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Build([]File{{"dir/a.txt", []byte("1")}, {"b.txt", []byte("2")}})
	if !bytes.Equal(a, b) {
		t.Fatal("bundle must not depend on input order")
	}
	dest := t.TempDir()
	names, err := Extract(bytes.NewReader(a), dest, DefaultLimits)
	if err != nil || len(names) != 2 {
		t.Fatalf("%v %v", names, err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "dir", "a.txt"))
	if string(got) != "1" {
		t.Fatal("content mismatch")
	}
}

func TestBuildRejectsUnsafeNames(t *testing.T) {
	for _, n := range []string{"../x", "/etc/passwd", "a/../../b", "a//b", ""} {
		if _, err := Build([]File{{n, nil}}); err == nil {
			t.Errorf("%q: expected error", n)
		}
	}
}

func TestExtractRejectsSymlinkAndTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: "link", Linkname: "/etc"})
	tw.Close()
	if _, err := Extract(&buf, t.TempDir(), DefaultLimits); err == nil {
		t.Fatal("symlink must be rejected")
	}
	buf.Reset()
	tw = tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "../evil", Size: 1})
	tw.Write([]byte("x"))
	tw.Close()
	if _, err := Extract(&buf, t.TempDir(), DefaultLimits); err == nil {
		t.Fatal("traversal must be rejected")
	}
}

func TestExtractLimits(t *testing.T) {
	data, _ := Build([]File{{"a", make([]byte, 100)}})
	if _, err := Extract(bytes.NewReader(data), t.TempDir(), Limits{MaxFiles: 10, MaxTotalBytes: 50}); err == nil {
		t.Fatal("size limit must apply")
	}
}

func TestRoot(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 10)
	d1 := Digest(data, 3)
	d2 := Digest(data, 3)
	if d1 != d2 {
		t.Fatal("digest must be deterministic")
	}
	if Digest(data, 4) == d1 {
		t.Fatal("chunking is part of the digest")
	}
	if Digest(nil, 3) == Digest([]byte{}, 3) && Digest(nil, 3) == "" {
		t.Fatal("empty digest must be defined")
	}
	if NumChunks(10, 3) != 4 || NumChunks(0, 3) != 0 {
		t.Fatal("NumChunks")
	}
}
