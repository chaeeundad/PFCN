// Package bundle builds deterministic tar bundles and computes their chunk
// Merkle digests (spec §16.4).
package bundle

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lukechampine.com/blake3"
)

// ChunkSize is the transfer chunk size for plaintext bundles.
const ChunkSize = 1 << 20

// Limits bound extraction of untrusted bundles.
type Limits struct {
	MaxFiles      int
	MaxTotalBytes int64
}

// DefaultLimits apply when the caller has no tighter policy.
var DefaultLimits = Limits{MaxFiles: 10000, MaxTotalBytes: 64 << 30}

// File is one bundle entry.
type File struct {
	Name string // slash-separated relative path
	Data []byte
}

// Build writes a deterministic tar: entries sorted by name, fixed mode,
// zero owner and mtime.
func Build(files []File) ([]byte, error) {
	sorted := append([]File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	seen := map[string]bool{}
	for _, f := range sorted {
		if err := validName(f.Name); err != nil {
			return nil, err
		}
		if seen[f.Name] {
			return nil, fmt.Errorf("bundle: duplicate entry %q", f.Name)
		}
		seen[f.Name] = true
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     f.Name,
			Mode:     0o644,
			Size:     int64(len(f.Data)),
			ModTime:  time.Unix(0, 0).UTC(),
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("bundle: %s: %w", f.Name, err)
		}
		if _, err := tw.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// FilesFromDir reads every regular file under dir (recursively) as bundle
// entries named relative to dir, with prefix prepended.
func FilesFromDir(dir, prefix string) ([]File, error) {
	var out []File
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("bundle: %s is not a regular file", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, File{Name: path.Join(prefix, filepath.ToSlash(rel)), Data: data})
		return nil
	})
	return out, err
}

// Extract unpacks an untrusted tar into dest. Only regular files with safe
// relative names are accepted.
func Extract(r io.Reader, dest string, lim Limits) ([]string, error) {
	tr := tar.NewReader(r)
	var names []string
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names, nil
		}
		if err != nil {
			return nil, fmt.Errorf("bundle: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("bundle: entry %q is not a regular file", hdr.Name)
		}
		if err := validName(hdr.Name); err != nil {
			return nil, err
		}
		if len(names) >= lim.MaxFiles {
			return nil, errors.New("bundle: too many files")
		}
		total += hdr.Size
		if hdr.Size < 0 || total > lim.MaxTotalBytes {
			return nil, errors.New("bundle: size limit exceeded")
		}
		target := filepath.Join(dest, filepath.FromSlash(hdr.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("bundle: %w", err)
		}
		if _, err := io.CopyN(f, tr, hdr.Size); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		names = append(names, hdr.Name)
	}
}

func validName(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") ||
		path.Clean(name) != name || name == "." || strings.HasPrefix(name, "../") || name == ".." {
		return fmt.Errorf("bundle: unsafe entry name %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "" {
			return fmt.Errorf("bundle: unsafe entry name %q", name)
		}
	}
	return nil
}

// ChunkHashes returns the BLAKE3 leaf hash of each chunkSize chunk of r.
func ChunkHashes(r io.Reader, chunkSize int) ([][32]byte, int64, error) {
	var hashes [][32]byte
	var size int64
	buf := make([]byte, chunkSize)
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			hashes = append(hashes, LeafHash(buf[:n]))
			size += int64(n)
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return hashes, size, nil
		}
		if err != nil {
			return nil, 0, err
		}
	}
}

// LeafHash is blake3(0x00 || chunk).
func LeafHash(chunk []byte) [32]byte {
	h := blake3.New(32, nil)
	h.Write([]byte{0})
	h.Write(chunk)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Root computes the bundle digest from leaf hashes and total size:
//
//	node  = blake3(0x01 || left || right), odd node promoted
//	root  = blake3(0x02 || uint64be(size) || merkle)
func Root(leaves [][32]byte, size int64) string {
	level := append([][32]byte(nil), leaves...)
	var merkle [32]byte
	if len(level) > 0 {
		for len(level) > 1 {
			var next [][32]byte
			for i := 0; i < len(level); i += 2 {
				if i+1 == len(level) {
					next = append(next, level[i])
					continue
				}
				h := blake3.New(32, nil)
				h.Write([]byte{1})
				h.Write(level[i][:])
				h.Write(level[i+1][:])
				var n [32]byte
				copy(n[:], h.Sum(nil))
				next = append(next, n)
			}
			level = next
		}
		merkle = level[0]
	}
	h := blake3.New(32, nil)
	h.Write([]byte{2})
	var sz [8]byte
	binary.BigEndian.PutUint64(sz[:], uint64(size))
	h.Write(sz[:])
	h.Write(merkle[:])
	return "blake3:" + hex.EncodeToString(h.Sum(nil))
}

// Digest returns the chunked bundle digest of data.
func Digest(data []byte, chunkSize int) string {
	leaves, size, _ := ChunkHashes(bytes.NewReader(data), chunkSize)
	return Root(leaves, size)
}

// FileDigest returns the chunk hashes, size, and chunked digest of a file.
func FileDigest(p string, chunkSize int) ([][32]byte, int64, string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, "", err
	}
	defer f.Close()
	leaves, size, err := ChunkHashes(f, chunkSize)
	if err != nil {
		return nil, 0, "", err
	}
	return leaves, size, Root(leaves, size), nil
}

// NumChunks returns how many chunks a size splits into.
func NumChunks(size int64, chunkSize int) int {
	return int((size + int64(chunkSize) - 1) / int64(chunkSize))
}
