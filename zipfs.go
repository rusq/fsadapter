package fsadapter

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

var _ FSCloser = &ZIP{}

// ZIP is a filesystem adapter for zip files.
type ZIP struct {
	zw     *zip.Writer
	mu     sync.Mutex
	f      *os.File
	seen   map[string]bool // seen holds the list of seen directories.
	active *syncWriter
	cond   sync.Cond
	closed bool // guarded by mu
}

func (z *ZIP) String() string {
	if z.f == nil {
		return "<zip archive>"
	}
	return fmt.Sprintf("<zip archive: %s>", z.f.Name())
}

// NewZIP returns a new ZIP filesystem adapter for a given ZipWriter.
func NewZIP(zw *zip.Writer) *ZIP {
	return &ZIP{zw: zw, seen: make(map[string]bool)}
}

// NewZipFile returns a new ZIP filesystem adapter for a given filename.
func NewZipFile(filename string) (*ZIP, error) {
	f, err := os.Create(filename)
	if err != nil {
		return nil, err
	}
	zw := zip.NewWriter(f)
	return &ZIP{zw: zw, f: f, seen: make(map[string]bool)}, nil
}

// ErrEntryOpen reports that closing an archive opened with NewZipFile
// finalized an entry before its writer was closed.
var ErrEntryOpen = errors.New("ZIP archive closed with an open entry")

func normalizeZIPPath(name string) (string, bool, error) {
	slashed := strings.ReplaceAll(name, `\`, "/")
	directory := strings.HasSuffix(slashed, "/")
	clean := path.Clean(slashed)
	switch {
	case strings.HasPrefix(slashed, "/"):
		return "", false, invalidZIPPath(name, "rooted path")
	case strings.ContainsRune(name, 0):
		return "", false, invalidZIPPath(name, "path contains a NUL byte")
	case hasDrivePrefix(slashed) || hasDrivePrefix(clean):
		return "", false, invalidZIPPath(name, "drive-qualified path")
	case clean == ".":
		return "", false, invalidZIPPath(name, "path resolves to the archive root")
	case clean == ".." || strings.HasPrefix(clean, "../"):
		return "", false, invalidZIPPath(name, "path escapes the archive root")
	}
	for _, component := range strings.Split(clean, "/") {
		if strings.Trim(component, ". ") == "" {
			return "", false, invalidZIPPath(name, "path contains a component made only of dots and spaces")
		}
	}
	return clean, directory, nil
}

func hasDrivePrefix(name string) bool {
	return len(name) >= 2 && name[1] == ':' &&
		(('A' <= name[0] && name[0] <= 'Z') || ('a' <= name[0] && name[0] <= 'z'))
}

func invalidZIPPath(name, reason string) error {
	return fmt.Errorf("%w: %q: %s", ErrIllegalDir, name, reason)
}

// waitForEntry waits for the active writer to close. The caller holds z.mu.
func (z *ZIP) waitForEntry() error {
	if z.cond.L == nil {
		z.cond.L = &z.mu
	}
	for z.active != nil && !z.closed {
		z.cond.Wait()
	}
	if z.closed {
		return os.ErrClosed
	}
	return nil
}

// Create creates a new file in the ZIP archive, waiting for any active entry
// to close. The caller must close the returned writer when done.
// Both / and backslash are path separators on every OS.
// Rejected names wrap [ErrIllegalDir]. With a valid name, Create returns
// [os.ErrClosed] after an owned archive has closed.
func (z *ZIP) Create(filename string) (io.WriteCloser, error) {
	name, _, err := normalizeZIPPath(filename)
	if err != nil {
		return nil, err
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.waitForEntry(); err != nil {
		return nil, err
	}
	w, err := z.create(name)
	if err != nil {
		return nil, err
	}
	entry := &syncWriter{z: z, w: w}
	z.active = entry
	return entry, nil
}

// create creates a new file in the zip archive.
func (z *ZIP) create(filename string) (io.Writer, error) {
	if err := z.ensureDir(strings.TrimSuffix(filename, "/")); err != nil {
		return nil, err
	}
	header := &zip.FileHeader{
		Name:     filename,
		Method:   zip.Deflate,
		Modified: time.Now(),
	}
	return z.zw.CreateHeader(header)
}

// ensureDir ensures that the given directory exists in the zip archive.  This
// is necessary because if the empty directory is not created beforehand, the
// files will appear in the root of the zip archive, but will contain the
// slash in their name.  Most of the ZIP unarchivers will be able to handle
// this, but some will not.
func (z *ZIP) ensureDir(filename string) error {
	if z.seen == nil {
		z.seen = make(map[string]bool, 0)
	}
	ensureFn := func(dir string) error {
		if _, seen := z.seen[dir]; seen {
			return nil
		}
		// not seen, create an empty directory.
		if _, err := z.zw.Create(dir); err != nil {
			return err
		}
		z.seen[dir] = true
		return nil
	}
	dir, _ := path.Split(filename)
	for _, d := range z.dirpath(dir) {
		if err := ensureFn(d); err != nil {
			return err
		}
	}
	return nil
}

// dirpath decomposes the path into a list of directories that lead to a
// given directory, to ensure that the ensureDir would create all the nested
// levels.  For example, if the directory is "a/b/c", the returned list will
// be ["a/", "a/b/", "a/b/c/"].  The returned list is sorted from the root
// directory to the given directory.
func (*ZIP) dirpath(dir string) []string {
	const sep = "/"
	if len(dir) == 0 {
		return nil
	}
	var ret []string
	d := strings.TrimRight(dir, sep)
	for len(d) > 0 {
		ret = append([]string{strings.TrimRight(d, sep) + sep}, ret...)
		d, _ = path.Split(strings.TrimRight(d, sep))
	}
	return ret
}

// WriteFile writes data to a ZIP entry, waiting for any active entry to close.
// Both / and backslash are path separators on every OS. A trailing separator
// denotes a directory entry, which must have empty data.
// Rejected names and directory entries with data wrap [ErrIllegalDir].
// With valid arguments, WriteFile returns [os.ErrClosed] after an owned archive
// has closed.
func (z *ZIP) WriteFile(filename string, data []byte, _ os.FileMode) error {
	name, directory, err := normalizeZIPPath(filename)
	if err != nil {
		return err
	}
	if directory && len(data) != 0 {
		return invalidZIPPath(filename, "directory entry cannot have data")
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.waitForEntry(); err != nil {
		return err
	}
	if directory {
		name += "/"
		if z.seen[name] {
			return nil
		}
		if _, err := z.create(name); err != nil {
			return err
		}
		z.seen[name] = true
		return nil
	}
	zf, err := z.create(name)
	if err != nil {
		return err
	}

	_, err = io.Copy(zf, bytes.NewReader(data))
	return err
}

// Close closes the underlying zip writer and the file handle.  It is only
// necessary if ZIP was initialised using NewZipFile. Close waits for any
// in-progress write, including slow or stalled underlying I/O. It finalizes
// any open entry with the bytes written so far and reports ErrEntryOpen.
// Subsequent calls return nil.
func (z *ZIP) Close() error {
	if !z.ourHandles() {
		// we don't own the handles, so just bail out.
		return nil
	}
	z.mu.Lock()
	defer z.mu.Unlock()

	if z.closed {
		return nil
	}
	z.closed = true
	var entryErr error
	if z.active != nil {
		entryErr = ErrEntryOpen
		z.active.closeErr = entryErr
		z.active = nil
	}
	z.cond.Broadcast()
	err := z.closeHandles()
	if entryErr != nil {
		if err != nil {
			return errors.Join(entryErr, err)
		}
		return entryErr
	}
	return err
}

// closeHandles closes the underlying zip writer and the file handle.
func (z *ZIP) closeHandles() error {
	zipErr := z.zw.Close()
	fileErr := z.f.Close()
	if zipErr == nil {
		return fileErr
	}
	if fileErr == nil {
		return zipErr
	}
	return errors.Join(zipErr, fileErr)
}

// ourHandles returns true if we own the file handle.
func (z *ZIP) ourHandles() bool {
	return z.f != nil
}

// syncWriter serializes entry operations through the archive mutex.
type syncWriter struct {
	z        *ZIP
	w        io.Writer
	closeErr error // guarded by z.mu
}

func (sw *syncWriter) Write(p []byte) (int, error) {
	sw.z.mu.Lock()
	defer sw.z.mu.Unlock()
	if sw.z.active != sw || sw.z.closed {
		return 0, errors.New("file already closed")
	}
	return sw.w.Write(p)
}

func (sw *syncWriter) Close() error {
	sw.z.mu.Lock()
	defer sw.z.mu.Unlock()
	if sw.z.active == sw && !sw.z.closed {
		sw.z.active = nil
		sw.z.cond.Broadcast()
	}
	err := sw.closeErr
	sw.closeErr = nil
	return err
}
