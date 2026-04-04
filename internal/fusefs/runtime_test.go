package fusefs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestRuntimeSaveLoadDelete(t *testing.T) {
	runtime := NewRuntime(t.TempDir())
	state := MountState{
		Profile:    "default",
		AgentID:    "agent-123",
		Mountpoint: "/tmp/agentic9-agent-123",
		PID:        4242,
		Status:     MountStatusMounted,
	}
	if err := runtime.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := runtime.Load(state.Mountpoint)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Profile != state.Profile || got.AgentID != state.AgentID || got.Mountpoint != state.Mountpoint {
		t.Fatalf("unexpected state: %#v", got)
	}
	if got.PID != state.PID || got.Status != state.Status {
		t.Fatalf("unexpected pid/status: %#v", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt was not populated")
	}

	if err := runtime.Delete(state.Mountpoint); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := runtime.Load(state.Mountpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load after Delete error = %v, want os.ErrNotExist", err)
	}
}

func TestRuntimeWaitForMount(t *testing.T) {
	runtime := NewRuntime(t.TempDir())
	mountpoint := "/tmp/agentic9-ready"

	go func() {
		_ = runtime.Save(MountState{
			Profile:    "default",
			AgentID:    "agent-123",
			Mountpoint: mountpoint,
			PID:        os.Getpid(),
			Status:     MountStatusStarting,
		})
		time.Sleep(50 * time.Millisecond)
		_ = runtime.Save(MountState{
			Profile:    "default",
			AgentID:    "agent-123",
			Mountpoint: mountpoint,
			PID:        os.Getpid(),
			Status:     MountStatusMounted,
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := runtime.WaitForMount(ctx, mountpoint, os.Getpid())
	if err != nil {
		t.Fatalf("WaitForMount: %v", err)
	}
	if got.Status != MountStatusMounted {
		t.Fatalf("status = %q, want %q", got.Status, MountStatusMounted)
	}
}

func TestRuntimeWaitForMountFailure(t *testing.T) {
	runtime := NewRuntime(t.TempDir())
	mountpoint := "/tmp/agentic9-failed"
	if err := runtime.Save(MountState{
		Profile:    "default",
		AgentID:    "agent-123",
		Mountpoint: mountpoint,
		PID:        os.Getpid(),
		Status:     MountStatusFailed,
		Error:      "boom",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := runtime.WaitForMount(ctx, mountpoint, os.Getpid())
	if err == nil || err.Error() != "boom" {
		t.Fatalf("WaitForMount error = %v, want boom", err)
	}
}
