package fusefs

import (
	"errors"
	"syscall"
	"testing"

	"agentic9/internal/exportfs"
)

func TestErrnoMappings(t *testing.T) {
	if got := errno(exportfs.ErrNotSupported); got != syscall.ENOSYS {
		t.Fatalf("errno(ErrNotSupported) = %v, want %v", got, syscall.ENOSYS)
	}
	if got := errno(exportfs.ErrDisconnected); got != syscall.EIO {
		t.Fatalf("errno(ErrDisconnected) = %v, want %v", got, syscall.EIO)
	}
	if got := errno(errors.New("other")); got != syscall.EIO {
		t.Fatalf("errno(other) = %v, want %v", got, syscall.EIO)
	}
}
