package agent

import (
	"errors"
	"fmt"
	"os"

	"github.com/chaeeundad/PFCN/internal/bundle"
)

// receiver assembles a chunked file with per-chunk verification and a
// persisted have-map, so interrupted transfers resume (§16.4).
type receiver struct {
	f         *os.File
	havePath  string
	have      []byte
	leaves    [][32]byte
	size      int64
	chunkSize int
}

func openReceiver(path, havePath string, size int64, chunkSize int, leaves [][32]byte) (*receiver, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return nil, err
	}
	have, err := os.ReadFile(havePath)
	if err != nil || len(have) != len(leaves) {
		have = make([]byte, len(leaves))
	}
	return &receiver{f: f, havePath: havePath, have: have, leaves: leaves, size: size, chunkSize: chunkSize}, nil
}

func (r *receiver) Missing() []uint32 {
	var m []uint32
	for i, h := range r.have {
		if h == 0 {
			m = append(m, uint32(i))
		}
	}
	return m
}

func (r *receiver) Complete() bool {
	for _, h := range r.have {
		if h == 0 {
			return false
		}
	}
	return true
}

func (r *receiver) Put(index uint32, data []byte) error {
	i := int(index)
	if i >= len(r.leaves) {
		return errors.New("chunk index out of range")
	}
	off := int64(i) * int64(r.chunkSize)
	want := min(int64(r.chunkSize), r.size-off)
	if int64(len(data)) != want {
		return fmt.Errorf("chunk %d has %d bytes, want %d", i, len(data), want)
	}
	if bundle.LeafHash(data) != r.leaves[i] {
		return fmt.Errorf("chunk %d hash mismatch", i)
	}
	if _, err := r.f.WriteAt(data, off); err != nil {
		return err
	}
	r.have[i] = 1
	return os.WriteFile(r.havePath, r.have, 0o600)
}

func (r *receiver) Close() error { return r.f.Close() }
