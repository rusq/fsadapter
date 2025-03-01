package fsadapter

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FS is interface for operating on the files of the underlying filesystem.
type FS interface {
	Create(string) (io.WriteCloser, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
}

// FSCloser is an [FS] that can be closed.
//
//go:generate mockgen -destination mocks/mock_fsadapter/mock_fs.go github.com/rusq/fsadapter FSCloser
type FSCloser interface {
	FS
	io.Closer
}

type options struct {
	// forceZIP forces to use ZIP adapter on NULL location.
	forceZIP bool
}

// Option is a functional option for [New].
type Option func(*options)

// ForceZIP forces to use ZIP adapter on NULL location.
func ForceZIP() Option {
	return func(o *options) {
		o.forceZIP = true
	}
}

// New returns appropriate filesystem based on the name of the location.
// Logic is simple:
//   - if location is "/dev/null", NOP adapter is returned, unless ForceZIP
//     option is given, in which case ZIP adapter is returned over /dev/null.
//   - if location has a known extension, the appropriate adapter is returned.
//   - else: it's a directory.
//
// Currently supported extensions: ".zip" (case insensitive)
func New(location string, opt ...Option) (FSCloser, error) {
	o := options{}
	for _, f := range opt {
		f(&o)
	}
	if location == os.DevNull {
		// may be useful for testing.
		if o.forceZIP {
			return NewZipFile(location)
		}
		return NewNOP(), nil
	}
	switch strings.ToUpper(filepath.Ext(location)) {
	case ".ZIP":
		return NewZipFile(location)
	default:
		return NewDirectory(location), nil
	}
}
