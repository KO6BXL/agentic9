package workspace

import (
	"strings"
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

func TestValidateAgentID(t *testing.T) {
	valid := []string{
		"agent-123",
		"a",
		"A_b.c-9",
	}
	for _, agentID := range valid {
		if err := ValidateAgentID(agentID); err != nil {
			t.Fatalf("ValidateAgentID(%q): %v", agentID, err)
		}
	}

	invalid := []string{
		"",
		".",
		"..",
		"/tmp/x",
		"agent id",
		"_agent",
		"-agent",
		strings.Repeat("a", 65),
	}
	for _, agentID := range invalid {
		if err := ValidateAgentID(agentID); err == nil {
			t.Fatalf("ValidateAgentID(%q) unexpectedly succeeded", agentID)
		}
	}
}
