package protocol

import (
	"bytes"
	"testing"

	"github.com/chaeeundad/PFCN/internal/pb"
)

type rw struct {
	*bytes.Reader
	bytes.Buffer
}

func (r *rw) Read(p []byte) (int, error)  { return r.Reader.Read(p) }
func (r *rw) Write(p []byte) (int, error) { return r.Buffer.Write(p) }

// Frame decoding from untrusted peers must not panic or allocate unboundedly.
func FuzzRecvFrame(f *testing.F) {
	var buf bytes.Buffer
	NewConn(&buf).Send(&pb.Frame{Body: &pb.Frame_Chunk{Chunk: &pb.Chunk{Index: 1, Data: []byte("x")}}})
	f.Add(buf.Bytes())
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0x0f})
	f.Fuzz(func(t *testing.T, in []byte) {
		c := NewConn(&rw{Reader: bytes.NewReader(in)})
		for i := 0; i < 4; i++ {
			fr, err := c.Recv()
			if err != nil {
				return
			}
			if e, err := FromPB(fr.GetCapability()); err == nil {
				_ = e.Verify(SchemaCapability)
			}
		}
	})
}
