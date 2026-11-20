package fsadapter

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

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
	assert.NoError(t, z.Close())
	// test file closed

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

	zzz := ZIP{}

	found := false
	for _, f := range zr.File {
		t.Log(f.Name)
		if f.Name == zzz.normalizePath(fullpath) {
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
	t.Parallel()
	t.Run("issue#90", func(t *testing.T) {
		t.Parallel()
		// test for GH issue#90 - race condition in ZIP.Create
		const (
			numRoutines    = 16
			testContentsSz = 1 * (1 << 20)
		)

		var buf bytes.Buffer
		var wg sync.WaitGroup

		zw := zip.NewWriter(&buf)
		defer zw.Close()

		fsa := NewZIP(zw)
		defer fsa.Close()

		// prepare workers
		readySteadyGo := make(chan struct{})
		panicAttacks := make(chan any, numRoutines)

		for i := 0; i < numRoutines; i++ {
			wg.Add(1)
			go func(n int) {
				defer func() {
					if r := recover(); r != nil {
						panicAttacks <- fmt.Sprintf("ZIP.Create race condition in gr %d: %v", n, r)
					}
				}()

				defer wg.Done()
				var contents bytes.Buffer
				if _, err := io.CopyN(&contents, rand.Reader, testContentsSz); err != nil {
					panic(err)
				}

				<-readySteadyGo
				fw, err := fsa.Create(fmt.Sprintf("file%d", n))
				if err != nil {
					panic(err)
				}
				defer fw.Close()

				if _, err := io.Copy(fw, &contents); err != nil {
					panic(err)
				}
			}(i)
		}
		close(readySteadyGo)
		wg.Wait()
		close(panicAttacks)
		for r := range panicAttacks {
			if r != nil {
				t.Error(r)
			}
		}
	})
}

func TestZIP_normalizePath(t *testing.T) {
	type args struct {
		p string
	}
	tests := []struct {
		name string
		z    *ZIP
		args args
		want string
	}{
		{
			"windows",
			&ZIP{},
			args{filepath.Join("sample", "directory", "and", "file.txt")},
			"sample/directory/and/file.txt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.z.normalizePath(tt.args.p); got != tt.want {
				t.Errorf("ZIP.normalizePath() = %v, want %v", got, tt.want)
			}
		})
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

func Test_syncWriter_Close(t *testing.T) {
	t.Run("should unlock the mutex", func(t *testing.T) {
		sw := syncWriter{archiveMu: &sync.Mutex{}}

		sw.archiveMu.Lock()

		sw.Close()
		assert.True(t, sw.archiveMu.TryLock())
		assert.True(t, sw.closed)
	})
	t.Run("closing more than once does not panic", func(_ *testing.T) {
		sw := syncWriter{archiveMu: &sync.Mutex{}}
		sw.archiveMu.Lock()
		sw.Close()
		sw.Close()
	})
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

			err = z.Close()
			require.ErrorIs(t, err, finalizeErr)
			if fileAlreadyClosed {
				require.ErrorIs(t, err, os.ErrClosed)
			} else {
				require.Equal(t, finalizeErr, err)
				_, err = f.Stat()
				require.ErrorIs(t, err, os.ErrClosed)
			}
		})
	}
}

func TestSyncWriterConcurrentClose(t *testing.T) {
	var archiveMu sync.Mutex
	archiveMu.Lock()
	writeStarted := make(chan struct{})
	finishWrite := make(chan struct{})
	sw := &syncWriter{
		archiveMu: &archiveMu,
		w: zipTestWriter(func(p []byte) (int, error) {
			close(writeStarted)
			<-finishWrite
			return len(p), nil
		}),
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := sw.Write([]byte("entry"))
		writeDone <- err
	}()
	<-writeStarted

	if sw.mu.TryLock() {
		sw.mu.Unlock()
		t.Error("entry lock was available during the write")
	}

	var closes sync.WaitGroup
	for range 8 {
		closes.Go(func() { assert.NoError(t, sw.Close()) })
	}
	if archiveMu.TryLock() {
		archiveMu.Unlock()
		t.Error("archive lock was released during the write")
	}
	close(finishWrite)
	require.NoError(t, <-writeDone)
	closes.Wait()

	require.True(t, archiveMu.TryLock(), "Close did not release the archive lock")
	defer archiveMu.Unlock()
	_, err := sw.Write([]byte("closed"))
	require.ErrorContains(t, err, "file already closed")
}
