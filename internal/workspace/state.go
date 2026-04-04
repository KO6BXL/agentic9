package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"agentic9/internal/config"
)

type Metadata struct {
	Profile    string    `json:"profile"`
	AgentID    string    `json:"agent_id"`
	RemoteRoot string    `json:"remote_root"`
	Mountpoint string    `json:"mountpoint"`
	Mounted    bool      `json:"mounted"`
	MountPID   int       `json:"mount_pid,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type Manager struct {
	root string
}

func NewManager(root string) (*Manager, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Manager{root: root}, nil
}

func (m *Manager) Save(meta Metadata) error {
	path := m.path(meta.Profile, meta.AgentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (m *Manager) Load(profile, agentID string) (Metadata, error) {
	data, err := os.ReadFile(m.path(profile, agentID))
	if err != nil {
		return Metadata{}, err
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return Metadata{}, err
	}
	return meta, nil
}

func (m *Manager) Delete(profile, agentID string) error {
	return os.Remove(m.path(profile, agentID))
}

func (m *Manager) path(profile, agentID string) string {
	return filepath.Join(m.root, profile, agentID+".json")
}

func RemoteRoot(profile config.Profile, agentID string) string {
	return filepath.Join(profile.RemoteBase, agentID, "root")
}

func DefaultMountpoint(profile, agentID string) (string, error) {
	base := config.DefaultRuntimeRoot()
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(base, profile, agentID), nil
}

func ValidateAgentID(agentID string) error {
	if agentID == "" {
		return fmt.Errorf("agent id must not be empty")
	}
	if len(agentID) > 64 {
		return fmt.Errorf("agent id must be at most 64 characters")
	}
	if agentID == "." || agentID == ".." {
		return fmt.Errorf("agent id %q is not allowed", agentID)
	}
	first := agentID[0]
	if !isAgentIDAlphaNum(first) {
		return fmt.Errorf("agent id must start with an ASCII letter or digit")
	}
	for i := 0; i < len(agentID); i++ {
		ch := agentID[i]
		if isAgentIDAlphaNum(ch) {
			continue
		}
		switch ch {
		case '-', '_', '.':
		default:
			return fmt.Errorf("agent id may only contain ASCII letters, digits, '.', '_' or '-'")
		}
	}
	return nil
}

func isAgentIDAlphaNum(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}
