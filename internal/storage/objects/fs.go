package objects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrProduction: the filesystem adapter is for development and tests only
// and refuses to open where APP_ENV is production (D-044).
var ErrProduction = errors.New("objects: the filesystem adapter is for development and tests only")

// FS keeps objects as files under a root directory, one file per key
// (development and tests only, D-044). Files are written to a temporary
// name, synced and linked into place, so a reader sees a whole object or
// none, and an existing object is never replaced.
type FS struct {
	root string
}

var (
	_ Store      = (*FS)(nil)
	_ Maintainer = (*FS)(nil)
)

// NewFS opens the store at root, an existing directory given as an
// absolute path. env is the deployment's APP_ENV: "production" is refused.
func NewFS(root, env string) (*FS, error) {
	if env == "production" {
		return nil, ErrProduction
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, fmt.Errorf("objects: the root must be a clean absolute path")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("objects: the root cannot be used: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("objects: the root is not a directory")
	}
	return &FS{root: root}, nil
}

// path is the file of key. Keys are checked before they reach the file
// system: only tenant/<org>/... keys the server makes, so no segment is
// empty, "." or "..", and nothing in them is a separator but "/".
func (f *FS) path(key string) (string, error) {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) != 3 || parts[0] != "tenant" || !ValidKey(parts[1], key) {
		return "", ErrInvalidKey
	}
	p := filepath.Join(f.root, filepath.FromSlash(key))
	if rel, err := filepath.Rel(f.root, p); err != nil || rel != filepath.FromSlash(key) {
		return "", ErrInvalidKey
	}
	return p, nil
}

// Put writes r to key (see Store). The file appears under its name only
// once it is complete and synced.
func (f *FS) Put(ctx context.Context, key string, r io.Reader, max int64) (Object, error) {
	if err := ctx.Err(); err != nil {
		return Object{}, err
	}
	p, err := f.path(key)
	if err != nil {
		return Object{}, err
	}
	if max < 0 {
		return Object{}, fmt.Errorf("objects: a negative size limit")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return Object{}, fmt.Errorf("objects: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return Object{}, fmt.Errorf("objects: %w", err)
	}
	defer os.Remove(tmp.Name()) // a no-op once linked into place
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), &limited{ctx: ctx, r: r, left: max + 1})
	if err == nil && n > max {
		err = ErrTooLarge
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Object{}, err
	}
	// A hard link never replaces an existing name, unlike rename.
	if err := os.Link(tmp.Name(), p); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Object{}, ErrExists
		}
		return Object{}, fmt.Errorf("objects: %w", err)
	}
	if d, err := os.Open(filepath.Dir(p)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return Object{Key: key, SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}

// Get opens key (see Store).
func (f *FS) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := f.path(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("objects: %w", err)
	}
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, ErrNotFound
	}
	return &reader{ctx: ctx, f: file}, nil
}

// Delete removes key (maintenance only, see Maintainer).
func (f *FS) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := f.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

// limited reads at most left bytes of r, stopping when ctx ends.
type limited struct {
	ctx  context.Context
	r    io.Reader
	left int64
}

func (l *limited) Read(p []byte) (int, error) {
	if err := l.ctx.Err(); err != nil {
		return 0, err
	}
	if l.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	return n, err
}

// reader is an open object that stops when its context ends.
type reader struct {
	ctx context.Context
	f   *os.File
}

func (r *reader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.f.Read(p)
}

func (r *reader) Close() error { return r.f.Close() }
