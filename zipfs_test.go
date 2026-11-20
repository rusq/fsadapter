package fsadapter

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"io/fs"
	mrand "math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewZipFile(t *testing.T) {
	tmp := t.TempDir()
	t.Run("zip file is created", func(t *testing.T) {
		zipPath, zf := gimmeZIP(t, tmp)

		assert.NotNil(t, zf.zw)
		assert.NotNil(t, zf.f)

		require.NoError(t, zf.Close())
		require.NoError(t, zf.Close())
		require.FileExists(t, zipPath)
	})
	t.Run("error creating a file", func(t *testing.T) {
		zipPath := tmp // should fail on directory
		_, err := NewZipFile(zipPath)
		require.Error(t, err)
	})
}

func TestZIP_Create(t *testing.T) {
	tmp := t.TempDir()
	t.Run("file is created", func(t *testing.T) {
		testsuiteZipFile(t, filepath.Join(tmp, "test.zip"), "abc/def.txt", "abcdef")
	})
	t.Run("backslashes", func(t *testing.T) {
		testsuiteZipFile(t, filepath.Join(tmp, "test.zip"), "abc\\backslash.txt", "abcdef")
	})
}

func testsuiteZipFile(t *testing.T, zipFile, filename, content string) {
	hArc, err := os.Create(zipFile)
	require.NoError(t, err)
	defer hArc.Close()
	zw := zip.NewWriter(hArc)
	z := ZIP{zw: zw}

	// create test file
	hTest, err := z.Create(filename)
	require.NoError(t, err)
	_, err = io.Copy(hTest, strings.NewReader(content))
	assert.NoError(t, err)
	require.NoError(t, hTest.Close())
	assert.NoError(t, z.Close())

	assert.NoError(t, zw.Close())
	assert.NoError(t, hArc.Close())

	assertZippedFileSize(t, zipFile, filename, uint64(len(content)))
}

func assertZippedFileSize(t *testing.T, zipfile string, fullpath string, size uint64) {
	zr, err := zip.OpenReader(zipfile)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	wantName := strings.ReplaceAll(fullpath, "\\", "/")

	found := false
	for _, f := range zr.File {
		t.Log(f.Name)
		if f.Name == wantName {
			if f.UncompressedSize64 != size {
				t.Errorf("file: %s, size mismatch: want: %d, got %d", fullpath, size, f.UncompressedSize64)
				return
			}
			t.Log("size OK")
			return
		}
	}
	if !found {
		t.Errorf("file not found: %s", fullpath)
	}
}

func TestZIP_WriteFile(t *testing.T) {
	tmp := t.TempDir()

	t.Run("write file creates the file in the zip archive", func(t *testing.T) {
		zipfile, hZF := gimmeZIP(t, tmp)
		if err := hZF.WriteFile("test1.txt", []byte("0123456789abcdef"), 0o750); err != nil {
			hZF.Close()
			t.Fatalf("ZIP.WriteFile err=%s", err)
		}
		assert.NoError(t, hZF.Close())

		assertZippedFileSize(t, zipfile, "test1.txt", 16)
	})
}

// gimmeZIP creates a zip file, returns it's name and initialised *ZIP instance.
// Don't forget to close it before running assertions.
func gimmeZIP(t *testing.T, tmpdir string) (filename string, hZF *ZIP) {
	zipfile := filepath.Join(tmpdir, RandString(8)+".zip")
	hZF, err := NewZipFile(zipfile)
	if err != nil {
		t.Fatal(err)
	}
	return zipfile, hZF
}

func RandString(sz int) string {
	const (
		charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
		chrstSz = len(charset)
	)
	ret := make([]byte, sz)
	for i := 0; i < sz; i++ {
		ret[i] = charset[mrand.Intn(chrstSz)]
	}
	return string(ret)
}

func TestNewZIP(t *testing.T) {
	tmpdir := t.TempDir()
	t.Run("ensure it's the same zw", func(t *testing.T) {
		hFile, err := os.Create(filepath.Join(tmpdir, "x.zip"))
		assert.NoError(t, err)
		defer hFile.Close()

		zw := zip.NewWriter(hFile)
		zf := NewZIP(zw)

		assert.Equal(t, zw, zf.zw)
	})
}

func TestCreateConcurrency(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	z := NewZIP(zw)
	var workers sync.WaitGroup
	start := make(chan struct{})
	expected := make(map[string]string)
	for i := range 16 {
		name := fmt.Sprintf("file%d", i)
		content := fmt.Sprintf("content for entry %d", i)
		expected[name] = content
		workers.Go(func() {
			<-start
			w, err := z.Create(name)
			if assert.NoError(t, err) {
				_, err := io.WriteString(w, content)
				assert.NoError(t, err)
				assert.NoError(t, w.Close())
			}
		})
	}
	close(start)
	workers.Wait()
	require.NoError(t, zw.Close())
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	require.Len(t, zr.File, len(expected))
	for _, f := range zr.File {
		want, ok := expected[f.Name]
		require.True(t, ok, "unexpected or duplicate entry %q", f.Name)
		r, err := f.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(r)
		require.NoError(t, r.Close())
		require.NoError(t, err)
		require.Equal(t, want, string(data))
		delete(expected, f.Name)
	}
}

func TestZIP_dirpath(t *testing.T) {
	type args struct {
		dir string
	}
	tests := []struct {
		name string
		z    *ZIP
		args args
		want []string
	}{
		{
			"single",
			&ZIP{},
			args{"foo/"},
			[]string{"foo/"},
		},
		{
			"single",
			&ZIP{},
			args{"foo"},
			[]string{"foo/"},
		},
		{
			"two",
			&ZIP{},
			args{"foo/bar"},
			[]string{"foo/", "foo/bar/"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.z.dirpath(tt.args.dir); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ZIP.dirpath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestZIPCreateRecoversFromError(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	z := NewZIP(zw)
	createErr := errors.New("compressor unavailable")
	zw.RegisterCompressor(zip.Deflate, func(io.Writer) (io.WriteCloser, error) {
		return nil, createErr
	})

	w, err := z.Create("first.txt")
	require.ErrorIs(t, err, createErr)
	require.Nil(t, w)
	require.True(t, z.mu.TryLock(), "failed Create left the archive locked")
	z.mu.Unlock()

	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.DefaultCompression)
	})
	require.NoError(t, z.WriteFile("second.txt", []byte("recovered"), 0o600))
	require.NoError(t, zw.Close())

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	require.Len(t, zr.File, 1)
	r, err := zr.File[0].Open()
	require.NoError(t, err)
	defer r.Close()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, "recovered", string(data))
}

type zipTestWriter func([]byte) (int, error)

func (w zipTestWriter) Write(p []byte) (int, error) {
	return w(p)
}

func TestZIPCloseFinalizationFailure(t *testing.T) {
	finalizeErr := errors.New("ZIP output failed")
	for _, fileAlreadyClosed := range []bool{false, true} {
		t.Run(fmt.Sprintf("file already closed=%t", fileAlreadyClosed), func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "*.zip")
			require.NoError(t, err)
			t.Cleanup(func() { _ = f.Close() })
			if fileAlreadyClosed {
				require.NoError(t, f.Close())
			}
			z := NewZIP(zip.NewWriter(zipTestWriter(func([]byte) (int, error) {
				return 0, finalizeErr
			})))
			z.f = f
			w, err := z.Create("open.txt")
			require.NoError(t, err)

			err = z.Close()
			require.ErrorIs(t, err, ErrEntryOpen)
			require.ErrorIs(t, w.Close(), ErrEntryOpen)
			require.ErrorIs(t, err, finalizeErr)
			require.NoError(t, z.Close())
			if fileAlreadyClosed {
				require.ErrorIs(t, err, os.ErrClosed)
			} else {
				_, err = f.Stat()
				require.ErrorIs(t, err, os.ErrClosed)
			}
		})
	}
}

func waitForZIPStacks(frame, reason string, count int) bool {
	stackBuffer := make([]byte, 64<<10)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); runtime.Gosched() {
		n := runtime.Stack(stackBuffer, true)
		if n == len(stackBuffer) {
			stackBuffer = make([]byte, len(stackBuffer)*2)
			continue
		}
		found := 0
		for _, stack := range strings.Split(string(stackBuffer[:n]), "\n\n") {
			if strings.Contains(stack, "["+reason+"]") && strings.Contains(stack, frame) {
				found++
			}
		}
		if found >= count {
			return true
		}
	}
	return false
}

func TestSyncWriterConcurrentClose(t *testing.T) {
	_, z := gimmeZIP(t, t.TempDir())
	writeStarted := make(chan struct{})
	finishWrite := make(chan struct{})
	entry := &syncWriter{z: z, w: zipTestWriter(func(p []byte) (int, error) {
		close(writeStarted)
		<-finishWrite
		return len(p), nil
	})}
	z.active = entry
	writeDone := make(chan error, 1)
	go func() {
		_, err := entry.Write([]byte("entry"))
		writeDone <- err
	}()
	<-writeStarted

	closed := make(chan error, 1)
	go func() { closed <- z.Close() }()
	blocked := waitForZIPStacks("(*ZIP).Close(", "sync.Mutex.Lock", 1)
	close(finishWrite)
	require.NoError(t, <-writeDone)
	require.ErrorIs(t, <-closed, ErrEntryOpen)
	require.True(t, blocked, "ZIP.Close must block until the in-flight Write returns")

	var closes sync.WaitGroup
	results := make(chan error, 8)
	for range cap(results) {
		closes.Go(func() { results <- entry.Close() })
	}
	closes.Wait()
	close(results)
	var invalidated int
	for err := range results {
		if err != nil {
			require.ErrorIs(t, err, ErrEntryOpen)
			invalidated++
		}
	}
	require.Equal(t, 1, invalidated)
}

func TestZIP_EntryWaiters(t *testing.T) {
	for _, archiveClose := range []bool{false, true} {
		t.Run(fmt.Sprintf("archive close=%t", archiveClose), func(t *testing.T) {
			_, z := gimmeZIP(t, t.TempDir())
			t.Cleanup(func() { _ = z.Close() })
			entry, err := z.Create("active.txt")
			require.NoError(t, err)

			done := make(chan error, 4)
			for i := range cap(done) {
				go func() {
					w, err := z.Create(fmt.Sprintf("waiting%d.txt", i))
					if err == nil {
						err = w.Close()
					}
					done <- err
				}()
			}
			require.True(t, waitForZIPStacks("(*ZIP).Create(", "sync.Cond.Wait", cap(done)))
			invalid := make(chan error, 1)
			go func() {
				_, err := z.Create("")
				invalid <- err
			}()
			require.ErrorIs(t, zipResult(t, invalid), ErrIllegalDir)
			go func() { invalid <- z.WriteFile("./", nil, 0) }()
			require.ErrorIs(t, zipResult(t, invalid), ErrIllegalDir)
			if archiveClose {
				require.ErrorIs(t, z.Close(), ErrEntryOpen)
			} else {
				require.NoError(t, entry.Close())
			}
			for range cap(done) {
				if archiveClose {
					require.ErrorIs(t, zipResult(t, done), os.ErrClosed)
				} else {
					require.NoError(t, zipResult(t, done))
				}
			}
		})
	}
}

func zipResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("ZIP operation did not return")
		return nil
	}
}

func TestZIP_CloseOpenEntry(t *testing.T) {
	filename, z := gimmeZIP(t, t.TempDir())
	entry, err := z.Create("active.txt")
	require.NoError(t, err)
	_, err = io.WriteString(entry, "written so far")
	require.NoError(t, err)
	require.ErrorIs(t, z.Close(), ErrEntryOpen)
	require.NoError(t, z.Close())
	_, err = io.WriteString(entry, "late write")
	require.ErrorContains(t, err, "file already closed")
	require.ErrorIs(t, entry.Close(), ErrEntryOpen)
	require.NoError(t, entry.Close())
	_, err = z.Create("after.txt")
	require.ErrorIs(t, err, os.ErrClosed)
	require.ErrorIs(t, z.WriteFile("after.txt", nil, 0), os.ErrClosed)

	zr, err := zip.OpenReader(filename)
	require.NoError(t, err)
	defer zr.Close()
	data, err := fs.ReadFile(zr, "active.txt")
	require.NoError(t, err)
	require.Equal(t, "written so far", string(data))
}

func TestZIP_EntryNames(t *testing.T) {
	for _, create := range []bool{false, true} {
		t.Run(fmt.Sprintf("Create=%t", create), func(t *testing.T) {
			filename, z := gimmeZIP(t, t.TempDir())
			write := func(name string) error {
				if !create {
					return z.WriteFile(name, []byte("data"), 0)
				}
				w, err := z.Create(name)
				if err != nil {
					return err
				}
				_, err = io.WriteString(w, "data")
				return errors.Join(err, w.Close())
			}
			for _, name := range []string{"", "./", "a/..", "/x", "../x", "a/../../x", "C:x", "x: todo.txt", "a/../C:x", "a\x00b", ".. /x", ".../x", "a/.. /b"} {
				err := write(name)
				require.ErrorIs(t, err, ErrIllegalDir)
				require.ErrorContains(t, err, fmt.Sprintf("%q", name))
			}
			err := z.WriteFile("nonempty/", []byte("data"), 0)
			require.ErrorIs(t, err, ErrIllegalDir)
			require.ErrorContains(t, err, `"nonempty/"`)
			for _, name := range []string{`dir\nested\file.txt`, "12:30 standup.txt", "..notes"} {
				require.NoError(t, write(name))
			}
			require.NoError(t, z.WriteFile("dir/", nil, 0))
			require.NoError(t, z.WriteFile("empty/", nil, 0))
			require.NoError(t, z.WriteFile(`empty\`, nil, 0))
			require.NoError(t, z.Close())

			zr, err := zip.OpenReader(filename)
			require.NoError(t, err)
			defer zr.Close()
			var names []string
			for _, f := range zr.File {
				names = append(names, f.Name)
			}
			require.ElementsMatch(t, []string{"dir/", "dir/nested/", "dir/nested/file.txt", "12:30 standup.txt", "..notes", "empty/"}, names)
			data, err := fs.ReadFile(zr, "dir/nested/file.txt")
			require.NoError(t, err)
			require.Equal(t, "data", string(data))
		})
	}
}

func TestZIP_String(t *testing.T) {
	require.Equal(t, "<zip archive>", NewZIP(zip.NewWriter(io.Discard)).String())
	filename, z := gimmeZIP(t, t.TempDir())
	require.NoError(t, z.Close())
	require.Equal(t, fmt.Sprintf("<zip archive: %s>", filename), z.String())
}
