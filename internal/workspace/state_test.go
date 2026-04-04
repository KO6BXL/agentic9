package workspace

import (
	"testing"
	"time"
)

func TestManagerRoundTripMetadata(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	want := Metadata{
		Profile:    "default",
		AgentID:    "agent-123",
		RemoteRoot: "/usr/glenda/agentic9/workspaces/agent-123/root",
		Mountpoint: "/tmp/agentic9-agent-123",
		Mounted:    true,
		MountPID:   4242,
		CreatedAt:  time.Unix(1700000000, 0).UTC(),
	}
	if err := manager.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := manager.Load(want.Profile, want.AgentID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("Load = %#v, want %#v", got, want)
	}
}
