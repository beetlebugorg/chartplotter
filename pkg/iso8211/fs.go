package iso8211

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"time"
)

// The parser reads files (a base cell + its sibling .001/.002… updates) through
// the stdlib io/fs.FS interface. Two implementations cover every caller:
//
//   - OSFS():  the OS filesystem. Unlike os.DirFS / fstest.MapFS it does NOT go
//     through fs.ValidPath, so it accepts the absolute/relative OS paths the CLI
//     and server already pass.
//   - MemFS:   a map-backed in-memory FS for parsing raw bytes (the wasm baker
//     and tests), with no filesystem at all.
//
// io/fs is chosen over afero specifically so the parser compiles for wasm/tinygo
// — afero's OsFs pulls in os.Chmod/os.Chown/syscall.EBADFD, which tinygo's wasm
// os package doesn't implement.

// OSFS returns an fs.FS backed by the OS filesystem (any path os.Open accepts).
func OSFS() fs.FS { return osFS{} }

type osFS struct{}

func (osFS) Open(name string) (fs.File, error) { return os.Open(name) }

// Exists reports whether name can be opened in fsys. Used to probe for the
// sequential update files (io/fs has no Exists).
func Exists(fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// MemFS is a minimal read-only in-memory fs.FS keyed by exact name (no
// fs.ValidPath restriction, so synthetic absolute keys like "/US5MD1MC.000"
// work). Suitable for parsing a handful of cell buffers.
type MemFS map[string][]byte

func (m MemFS) Open(name string) (fs.File, error) {
	data, ok := m[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &memFile{name: name, r: bytes.NewReader(data), size: int64(len(data))}, nil
}

type memFile struct {
	name string
	r    *bytes.Reader
	size int64
}

func (f *memFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *memFile) Close() error               { return nil }
func (f *memFile) Stat() (fs.FileInfo, error) { return memInfo{f.name, f.size}, nil }

type memInfo struct {
	name string
	size int64
}

func (i memInfo) Name() string       { return path.Base(i.name) }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return 0o444 }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }
