package fsadapter

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var _ FSCloser = Directory{}

type Directory struct {
	dir string
}

// NewDirectory returns a new Directory filesystem adapter for a given
// directory.
func NewDirectory(dir string) Directory {
	return Directory{dir: dir}
}

func (d Directory) String() string {
	return "<directory: " + d.dir + ">"
}

// Create creates a new file in the directory.  Any intermediate directories
// are created as needed.
//
// The file and intermediate directories are created through an [os.Root]
// opened on the directory, so that a symbolic link can not redirect the write
// outside of the directory.  This has the following consequences:
//   - symbolic links within the directory must be relative and resolve
//     inside the directory; absolute symbolic links are rejected, even if
//     they point inside the directory;
//   - the directory, and any existing subdirectory on the path, must be
//     readable by the process, not only writable and searchable (on Unix,
//     write-only "drop box" directories are not supported).
func (fs Directory) Create(fpath string) (io.WriteCloser, error) {
	rel, err := fs.relPath(fpath)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", fpath, err)
	}
	root, err := fs.openRoot(rel)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	// files opened from the root remain valid after the root is closed.
	return root.Create(rel)
}

// relPath returns the cleaned path of fpath relative to the directory. It
// returns ErrIllegalDir if fpath lexically references a location outside of
// the directory.
func (fs Directory) relPath(fpath string) (string, error) {
	base := fs.base()
	node := filepath.Join(base, fpath)
	if err := fs.ensureSubdir(node); err != nil {
		return "", err
	}
	return filepath.Rel(base, node)
}

// base returns the cleaned directory path.  An empty directory refers to the
// current working directory.
func (fs Directory) base() string {
	return filepath.Clean(fs.dir)
}

// openRoot ensures that the directory exists, opens it as [os.Root], and
// creates the parent directories of rel within that root.  The caller must
// close the returned root.
func (fs Directory) openRoot(rel string) (*os.Root, error) {
	base := fs.base()
	if err := mkdirAll(base); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	if parent := filepath.Dir(rel); parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			root.Close()
			return nil, err
		}
	}
	return root, nil
}

// ErrIllegalDir is returned, if the file path reference is outside of the
// working directory.
var ErrIllegalDir = errors.New("illegal file path reference outside of working directory")

// ensureSubdir ensures that the node is a subdirectory of
// the fs.dir, and returns ErrIllegalDir, if not.  This ensures
// that caller won't be able to do anything naughty.
func (fs Directory) ensureSubdir(node string) error {
	if rel, err := filepath.Rel(fs.dir, node); err != nil {
		return err
	} else if strings.HasPrefix(rel, "..") {
		return ErrIllegalDir
	}

	return nil
}

// mkdirAll creates a directory "name", if the directory exists, it does
// nothing.
func mkdirAll(name string) error {
	if name == "" {
		return errors.New("empty directory")
	}

	fi, err := os.Stat(name)
	if err == nil && fi.IsDir() {
		// exists and is a directory
		return nil
	}

	if err := os.MkdirAll(name, 0755); err != nil {
		return err
	}
	return nil
}

// WriteFile writes data to a file named by name. If the file does not exist,
// WriteFile creates it with permissions perm (before umask); otherwise
// WriteFile truncates it before writing.  Only the permission bits of perm
// (0o777) are used, other bits are ignored.  The same symbolic link and
// permission restrictions as for [Directory.Create] apply.
func (fs Directory) WriteFile(name string, data []byte, perm os.FileMode) error {
	rel, err := fs.relPath(name)
	if err != nil {
		return fmt.Errorf("WriteFile: %w", err)
	}
	root, err := fs.openRoot(rel)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.WriteFile(rel, data, perm&os.ModePerm)
}

// Close is a noop for Directory.
func (fs Directory) Close() error {
	return nil
}
