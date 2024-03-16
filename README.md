# fsadapter - File System adapter

[![Go Reference](https://pkg.go.dev/badge/github.com/rusq/fsadapter.svg)](https://pkg.go.dev/github.com/rusq/fsadapter)

## Purpose
"fsadapter" provides write-only adapters to various destinations.  There are
currently 2 adapters:

- Directory
- ZIP

It is meant to be a drop-in replacement for os.* functions for
[Slackdump](https://github.com/rusq/slackdump).

## Details

Each adapter exposes the following methods:

- Create(string) (io.WriteCloser, error)
- WriteFile(name string, data []byte, perm os.FileMode) error
- Close() error

## Testing
If you need to use a mock of "fsadapter.FSCloser", it's in the
mocks/mock_fsadapter package.
