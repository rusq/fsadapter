package fsadapter

import (
	"io"
	"io/fs"
)

var _ FSCloser = &NOP{}

// NOP is a filesystem adapter that does nothing.
type NOP struct{}

func (n *NOP) String() string {
	return "<nop>"
}

// NewNOP returns a new NOP filesystem adapter.
func NewNOP() *NOP {
	return &NOP{}
}

type nopWriteCloser struct{}

func (*nopWriteCloser) Write(p []byte) (n int, err error) {
	return len(p), nil
}

func (*nopWriteCloser) Close() error {
	return nil
}

func (*NOP) Create(string) (io.WriteCloser, error) {
	return &nopWriteCloser{}, nil
}

func (*NOP) WriteFile(string, []byte, fs.FileMode) error {
	return nil
}

func (*NOP) Close() error {
	return nil
}
