package blob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
)

// Dir stores objects as files below one directory. All access goes through os.Root, so no key can leave it
// (symlinks included); writes go to a temporary file first and are renamed into place.
type Dir struct{ root *os.Root }

var _ Store = (*Dir)(nil)

const tmpDir = ".tmp"

// NewDir opens (and creates) dir.
func NewDir(dir string) (*Dir, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if err := r.MkdirAll(tmpDir, 0o700); err != nil {
		_ = r.Close()
		return nil, err
	}
	return &Dir{root: r}, nil
}

// Close releases the directory.
func (d *Dir) Close() error { return d.root.Close() }

// Put implements Store.
func (d *Dir) Put(ctx context.Context, key string, data []byte) error {
	if !ValidKey(key) {
		return ErrBadKey
	}
	if len(data) > MaxObject {
		return ErrTooLarge
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.root.MkdirAll(path.Dir(key), 0o700); err != nil {
		return err
	}
	var rnd [12]byte
	_, _ = rand.Read(rnd[:])
	tmp := path.Join(tmpDir, hex.EncodeToString(rnd[:]))
	if err := d.root.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := d.root.Rename(tmp, key); err != nil {
		_ = d.root.Remove(tmp)
		return err
	}
	return nil
}

// Get implements Store.
func (d *Dir) Get(ctx context.Context, key string) ([]byte, error) {
	if !ValidKey(key) {
		return nil, ErrBadKey
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := d.root.Open(key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, MaxObject+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxObject {
		return nil, ErrTooLarge
	}
	return b, nil
}

// Delete implements Store.
func (d *Dir) Delete(ctx context.Context, key string) error {
	if !ValidKey(key) {
		return ErrBadKey
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
