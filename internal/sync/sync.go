package syncdir

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Options struct {
	Mirror bool
}

func CopyTree(src, dst string, opts Options) error {
	seen := map[string]struct{}{}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		seen[rel] = struct{}{}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode().IsDir():
			return ensureDir(target, info.Mode().Perm())
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyFile(path, target, dst, info.Mode().Perm())
		default:
			return nil
		}
	})
	if err != nil {
		return err
	}
	if opts.Mirror {
		return filepath.WalkDir(dst, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dst, path)
			if err != nil || rel == "." {
				return err
			}
			if _, ok := seen[rel]; ok {
				return nil
			}
			return os.RemoveAll(path)
		})
	}
	return nil
}

func copyFile(src, dst, root string, mode fs.FileMode) error {
	parent := filepath.Dir(dst)
	if parent != root {
		if err := ensureDir(parent, 0o755); err != nil {
			return err
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func ensureDir(path string, mode fs.FileMode) error {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.IsDir() {
			return nil
		}
		return fmt.Errorf("mkdir %s: %w", path, fs.ErrExist)
	case !errors.Is(err, os.ErrNotExist):
		return err
	default:
		return os.MkdirAll(path, mode)
	}
}

func skip(rel string) bool {
	return rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator))
}
