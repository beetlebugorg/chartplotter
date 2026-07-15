package plugin

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// install.go verifies and unpacks a plugin archive. A plugin is a zip whose
// plugin.json carries a `files` map of path→sha256 covering every other file, so
// verifying the manifest verifies the whole archive (spec §2). Signing + TOFU key
// pinning are Phase 3; v1 does content-hash verification only.

// InstallOptions tunes installer policy.
type InstallOptions struct {
	// AllowCore permits ids under the reserved core.* prefix. False for third-party
	// archives (the CLI/UI install path); true only for in-tree tooling.
	AllowCore bool
}

// VerifyArchive opens the zip at path, parses+validates plugin.json, and checks that
// every file listed in the manifest's `files` map exists with a matching sha256.
// Files present in the archive but absent from `files` are allowed (e.g. plugin.sig);
// files listed but missing, or with a hash mismatch, are an error.
func VerifyArchive(path string) (*Manifest, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer zr.Close()
	return verify(&zr.Reader)
}

func verify(zr *zip.Reader) (*Manifest, error) {
	mf := findFile(zr, "plugin.json")
	if mf == nil {
		return nil, fmt.Errorf("archive has no plugin.json")
	}
	mb, err := readZipFile(mf)
	if err != nil {
		return nil, fmt.Errorf("read plugin.json: %w", err)
	}
	m, err := ParseManifest(mb)
	if err != nil {
		return nil, err
	}
	for name, want := range m.Files {
		f := findFile(zr, name)
		if f == nil {
			return nil, fmt.Errorf("manifest lists %q but it is missing from the archive", name)
		}
		sum, err := hashZipFile(f)
		if err != nil {
			return nil, fmt.Errorf("hash %q: %w", name, err)
		}
		if !hashEqual(sum, want) {
			return nil, fmt.Errorf("hash mismatch for %q: archive has %s, manifest wants %s", name, sum, want)
		}
	}
	return m, nil
}

// Install verifies the archive and unpacks it to <pluginsDir>/<id>/<version>/,
// returning the manifest. It refuses to overwrite an already-unpacked version.
func Install(archivePath, pluginsDir string, opts InstallOptions) (*Manifest, error) {
	m, err := VerifyArchive(archivePath)
	if err != nil {
		return nil, err
	}
	if !opts.AllowCore && strings.HasPrefix(m.ID, CorePrefix) {
		return nil, fmt.Errorf("id %q uses the reserved %q prefix", m.ID, CorePrefix)
	}
	dest := filepath.Join(pluginsDir, m.ID, m.Version)
	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("%s@%s is already installed", m.ID, m.Version)
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	// Unpack into a temp dir then rename, so a failed unpack leaves nothing partial.
	tmp := dest + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := unpack(&zr.Reader, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	return m, nil
}

// unpack writes every archive file under dest, rejecting path traversal.
func unpack(zr *zip.Reader, dest string) error {
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		clean, ok := safeJoin(dest, f.Name)
		if !ok {
			return fmt.Errorf("unsafe archive path %q", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
			return err
		}
		if err := extractOne(f, clean); err != nil {
			return err
		}
	}
	return nil
}

func extractOne(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	// Native entry points need the executable bit; everything else is 0644.
	mode := os.FileMode(0o644)
	if strings.HasPrefix(f.Name, "bin/") {
		mode = 0o755
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, io.LimitReader(rc, maxLine*4)) // generous per-file cap
	return err
}

// safeJoin joins base and a possibly-hostile archive path, returning ok=false if the
// result escapes base.
func safeJoin(base, name string) (string, bool) {
	clean := filepath.Join(base, filepath.Clean("/"+name))
	if clean != base && !strings.HasPrefix(clean, base+string(os.PathSeparator)) {
		return "", false
	}
	return clean, true
}

func findFile(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxLine))
}

func hashZipFile(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashEqual compares a computed hex digest against a manifest value, tolerating an
// optional "sha256:" prefix and case differences.
func hashEqual(got, want string) bool {
	want = strings.TrimPrefix(want, "sha256:")
	return strings.EqualFold(got, want)
}
