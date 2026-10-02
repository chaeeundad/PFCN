package protocol

import (
	"bufio"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/encoding/protodelim"

	"github.com/chaeeundad/PFCN/internal/pb"
)

// MaxFrameBytes bounds a single frame (chunks are 1 MiB).
const MaxFrameBytes = 8 << 20

// Conn reads and writes length-delimited frames on a stream.
type Conn struct {
	r *bufio.Reader
	w io.Writer
}

func NewConn(rw io.ReadWriter) *Conn {
	return &Conn{r: bufio.NewReaderSize(rw, 64<<10), w: rw}
}

func (c *Conn) Send(f *pb.Frame) error {
	_, err := protodelim.MarshalTo(c.w, f)
	return err
}

func (c *Conn) Recv() (*pb.Frame, error) {
	var f pb.Frame
	if err := (protodelim.UnmarshalOptions{MaxSize: MaxFrameBytes}).UnmarshalFrom(c.r, &f); err != nil {
		return nil, err
	}
	if e := f.GetError(); e != nil {
		return nil, &RemoteError{Code: e.Code, Message: e.Message}
	}
	return &f, nil
}

// SendError sends an error frame.
func (c *Conn) SendError(code string, err error) error {
	return c.Send(&pb.Frame{Body: &pb.Frame_Error{Error: &pb.Error{Code: code, Message: err.Error()}}})
}

// RemoteError is an error reported by the peer.
type RemoteError struct {
	Code    string
	Message string
}

func (e *RemoteError) Error() string { return fmt.Sprintf("peer: %s: %s", e.Code, e.Message) }

// IsRemote reports whether err is a RemoteError with code.
func IsRemote(err error, code string) bool {
	var re *RemoteError
	return errors.As(err, &re) && re.Code == code
}

// Error codes.
const (
	ErrCodeRejected     = "rejected"
	ErrCodeInvalid      = "invalid"
	ErrCodeUnauthorized = "unauthorized"
	ErrCodeNotFound     = "not_found"
	ErrCodeInternal     = "internal"
)
