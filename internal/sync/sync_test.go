package syncdir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDirExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := ensureDir(dir, 0o755); err != nil {
		t.Fatalf("ensureDir(existing dir): %v", err)
	}
}

func TestEnsureDirExistingFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err := ensureDir(file, 0o755)
	if err == nil {
		t.Fatal("ensureDir(existing file) unexpectedly succeeded")
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("ensureDir(existing file) = %v, want os.ErrExist", err)
	}
}

func TestCopyTreeCopiesTopLevelFilesIntoExistingDirectory(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}

	if err := CopyTree(src, dst, Options{}); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "hello.txt"))
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("unexpected dst data %q", string(data))
	}
}
