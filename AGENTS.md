# Repository Guidelines

## Project Structure

This repository is the Go module `github.com/rusq/fsadapter`. It provides write-only filesystem adapters for callers such as Slackdump. The root package defines `FSCloser` and the `New` factory in `fsadapter.go`. The directory, ZIP, and no-op implementations live in `directory.go`, `zipfs.go`, and `nop.go`. Tests sit alongside the source in `*_test.go` files. The generated GoMock implementation of `FSCloser` lives in `mocks/mock_fsadapter/mock_fs.go`.

## Build and Development

Use Go 1.25 or newer, as declared in `go.mod`. Run these commands from the repository root.

- `go build -v ./...` compiles the library and mock package.
- `go test -v ./...` runs the full suite.
- `go test -race ./...` checks for data races when changing ZIP locking or writer lifecycle code.
- `go generate ./...` refreshes the mock after an `FSCloser` change. Put `mockgen` v0.6.0 on your PATH first.

The GitHub Actions workflow runs the build and test commands on Ubuntu and reads the Go version from `go.mod`.

## Coding Style

Run `gofmt` on changed Go files. Use standard Go naming, tabs for indentation, and doc comments that start with the exported identifier. Regenerate the mock when its interface changes. Preserve directory root containment and ZIP writer close behavior when changing those adapters.

## Testing

Use Go’s `testing` package and the existing Testify `assert` and `require` helpers. Follow names such as `TestDirectory_Create` and `TestZIP_WriteFile`. Use `t.TempDir()` for filesystem fixtures and add focused regression tests for changes to path handling, concurrency, or error recovery. Run a focused test with `go test -run '^TestZIP_WriteFile$' .`. The workflow defines no minimum coverage percentage.

## Commits and Pull Requests

Recent commits use short, plain-language subjects such as “Add nop adapter.” In pull requests, describe the behavior change, list the checks you ran, and link any related issue. Call out changes to the public interface or adapter behavior so downstream callers can assess the impact.
