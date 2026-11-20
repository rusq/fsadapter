package fsadapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// symlinkFixture creates a base directory and an outside directory, and a
// symlink "link" inside the base directory, pointing to target (relative to
// the base directory, or absolute).  It returns the base and outside dirs.
func symlinkFixture(t *testing.T, target func(base, outside string) string) (base, outside string) {
	t.Helper()
	base, outside = t.TempDir(), t.TempDir()
	if err := os.Symlink(target(base, outside), filepath.Join(base, "link")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	return base, outside
}

func toOutside(_, outside string) string { return outside }

func TestDirectory_Create(t *testing.T) {
	tmpdir := t.TempDir()
	type fields struct {
		dir string
	}
	type args struct {
		fpath string
	}
	tests := []struct {
		name     string
		fields   fields
		args     args
		testData []byte
		wantErr  bool
	}{
		{
			"ensure file is created and data is written (root dir)",
			fields{dir: tmpdir},
			args{"testfile.txt"},
			[]byte("123"),
			false,
		},
		{
			"ensure file is created and data is written (subdir)",
			fields{dir: tmpdir},
			args{filepath.Join("ooooh", "testfile.txt")},
			[]byte("123"),
			false,
		},
		{
			"filename starts with two dots",
			fields{dir: tmpdir},
			args{"..notes"},
			[]byte("123"),
			false,
		},
		{
			"directory (error)",
			fields{dir: tmpdir},
			args{""},
			[]byte("123"),
			true,
		},
		{
			"invalid filename",
			fields{dir: tmpdir},
			args{".."},
			[]byte("123"),
			true,
		},
		{
			"outside of root directory",
			fields{dir: tmpdir},
			args{strings.Repeat(".."+string(filepath.Separator), 20) + filepath.Join("tmp", "hello_rootfs.txt")},
			[]byte("123"),
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := Directory{
				dir: tt.fields.dir,
			}
			f, err := fs.Create(tt.args.fpath)
			if (err != nil) != tt.wantErr {
				t.Errorf("Directory.Create() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			assert, require := assert.New(t), require.New(t)

			n, err := f.Write(tt.testData)
			require.NoError(err)
			assert.Equal(3, n)

			assert.NoError(f.Close())

			testFile := filepath.Join(tt.fields.dir, tt.args.fpath)
			assert.FileExists(testFile)

			fileData, err := os.ReadFile(testFile)
			assert.NoError(err)

			assert.Equal(tt.testData, fileData)
		})
	}

	t.Run("symlinked parent outside of root directory", func(t *testing.T) {
		base, outside := symlinkFixture(t, toOutside)
		f, err := NewDirectory(base).Create(filepath.Join("link", "authorized_keys"))
		if err == nil {
			f.Close()
		}
		assert.Error(t, err)
		assert.NoFileExists(t, filepath.Join(outside, "authorized_keys"))
	})
	t.Run("symlinked parent outside of root, nested subdirectory", func(t *testing.T) {
		base, outside := symlinkFixture(t, toOutside)
		f, err := NewDirectory(base).Create(filepath.Join("link", "sub", "file.txt"))
		if err == nil {
			f.Close()
		}
		assert.Error(t, err)
		assert.NoDirExists(t, filepath.Join(outside, "sub"))
	})
	t.Run("dangling file symlink outside of root directory", func(t *testing.T) {
		base, outside := symlinkFixture(t, func(_, outside string) string {
			return filepath.Join(outside, "target.txt")
		})
		f, err := NewDirectory(base).Create("link")
		if err == nil {
			f.Close()
		}
		assert.Error(t, err)
		assert.NoFileExists(t, filepath.Join(outside, "target.txt"))
	})
	t.Run("symlink within root directory is allowed", func(t *testing.T) {
		base, _ := symlinkFixture(t, func(base, _ string) string {
			dir := filepath.Join(base, "real")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return "real"
		})
		f, err := NewDirectory(base).Create(filepath.Join("link", "file.txt"))
		require.NoError(t, err)
		_, err = f.Write([]byte("123"))
		require.NoError(t, err)
		require.NoError(t, f.Close())
		assert.FileExists(t, filepath.Join(base, "real", "file.txt"))
	})
	t.Run("absolute symlink within root directory is rejected", func(t *testing.T) {
		base, _ := symlinkFixture(t, func(base, _ string) string {
			dir := filepath.Join(base, "real")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return dir
		})
		f, err := NewDirectory(base).Create(filepath.Join("link", "file.txt"))
		if err == nil {
			f.Close()
		}
		assert.Error(t, err)
		assert.NoFileExists(t, filepath.Join(base, "real", "file.txt"))
	})
	t.Run("empty directory is the current directory", func(t *testing.T) {
		base := t.TempDir()
		t.Chdir(base)
		f, err := NewDirectory("").Create("file.txt")
		require.NoError(t, err)
		require.NoError(t, f.Close())
		assert.FileExists(t, filepath.Join(base, "file.txt"))
	})
	t.Run("unclean directory is cleaned", func(t *testing.T) {
		base := t.TempDir()
		if err := os.WriteFile(filepath.Join(base, "notadir"), nil, 0o640); err != nil {
			t.Fatal(err)
		}
		f, err := NewDirectory(filepath.Join(base, "notadir") + "/../out").Create("file.txt")
		require.NoError(t, err)
		require.NoError(t, f.Close())
		assert.FileExists(t, filepath.Join(base, "out", "file.txt"))
	})
	t.Run("creates missing root directory", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "new", "root")
		f, err := NewDirectory(base).Create("file.txt")
		require.NoError(t, err)
		require.NoError(t, f.Close())
		assert.FileExists(t, filepath.Join(base, "file.txt"))
	})
}

func TestDirectory_ensureSubdir(t *testing.T) {
	type fields struct {
		dir string
	}
	type args struct {
		node string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			"is a subdir",
			fields{dir: filepath.Join("a", "b")},
			args{filepath.Join("a", "b", "c", "d", "e")},
			false,
		},
		{
			"is not a subdir",
			fields{dir: filepath.Join("a", "b", "d")},
			args{filepath.Join("a", "b", "c")},
			true,
		},
		{
			"path hack",
			fields{dir: filepath.Join("a", "b", "d")},
			args{filepath.Join("..", "..", "..", "tmp")},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := Directory{
				dir: tt.fields.dir,
			}
			if err := fs.ensureSubdir(tt.args.node); (err != nil) != tt.wantErr {
				t.Errorf("Directory.ensureSubdir() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_mkdirAll(t *testing.T) {
	tmpdir := t.TempDir()
	existingFile := filepath.Join(tmpdir, "existing.txt")
	if err := os.WriteFile(existingFile, []byte("123"), 0o640); err != nil {
		t.Fatal(err)
	}

	type args struct {
		name string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			"ok",
			args{filepath.Join(tmpdir, "abc")},
			false,
		},
		{
			"empty dir - error",
			args{""},
			true,
		},
		{
			"already exists",
			args{tmpdir},
			false,
		},
		{
			"is a file",
			args{existingFile},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := mkdirAll(tt.args.name); (err != nil) != tt.wantErr {
				t.Errorf("mkdirAll() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDirectory_WriteFile(t *testing.T) {
	tmpdir := t.TempDir()
	type fields struct {
		dir string
	}
	type args struct {
		name string
		data []byte
		perm os.FileMode
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			"all ok",
			fields{dir: tmpdir},
			args{"blah.txt", []byte("blah"), 0o640},
			false,
		},
		{
			"subdirectory starts with two dots",
			fields{dir: tmpdir},
			args{filepath.Join("..cache", "blah.txt"), []byte("blah"), 0o640},
			false,
		},
		{
			"outside of base path is an error",
			fields{dir: tmpdir},
			args{filepath.Join("..", "blah.txt"), []byte("blah"), 0o640},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := Directory{
				dir: tt.fields.dir,
			}
			err := fs.WriteFile(tt.args.name, tt.args.data, tt.args.perm)
			if (err != nil) != tt.wantErr {
				t.Errorf("Directory.WriteFile() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			// verify file contents
			data, err := os.ReadFile(filepath.Join(fs.dir, tt.args.name))
			if err != nil {
				t.Fatal(err)
			}
			assert.Equal(t, tt.args.data, data)
		})
	}

	t.Run("symlinked parent outside of root directory", func(t *testing.T) {
		base, outside := symlinkFixture(t, toOutside)
		err := NewDirectory(base).WriteFile(filepath.Join("link", "blah.txt"), []byte("blah"), 0o640)
		assert.Error(t, err)
		assert.NoFileExists(t, filepath.Join(outside, "blah.txt"))
	})
	t.Run("non-permission mode bits are ignored", func(t *testing.T) {
		base := t.TempDir()
		err := NewDirectory(base).WriteFile("blah.txt", []byte("blah"), 0o640|os.ModeTemporary)
		require.NoError(t, err)
		fi, err := os.Stat(filepath.Join(base, "blah.txt"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0), fi.Mode()&^os.ModePerm)
	})
}
