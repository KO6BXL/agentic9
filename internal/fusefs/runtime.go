package fusefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type MountStatus string

const (
	MountStatusStarting MountStatus = "starting"
	MountStatusMounted  MountStatus = "mounted"
	MountStatusFailed   MountStatus = "failed"
)

type MountState struct {
	Profile    string      `json:"profile"`
	AgentID    string      `json:"agent_id"`
	Mountpoint string      `json:"mountpoint"`
	PID        int         `json:"pid"`
	Status     MountStatus `json:"status"`
	Error      string      `json:"error,omitempty"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

type Runtime struct {
	root string
}

func NewRuntime(root string) *Runtime {
	return &Runtime{root: filepath.Join(root, "mounts")}
}

func (r *Runtime) Save(state MountState) error {
	if err := os.MkdirAll(r.root, 0o755); err != nil {
		return err
	}
	state.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.statePath(state.Mountpoint), data, 0o644)
}

func (r *Runtime) Load(mountpoint string) (MountState, error) {
	data, err := os.ReadFile(r.statePath(mountpoint))
	if err != nil {
		return MountState{}, err
	}
	var state MountState
	if err := json.Unmarshal(data, &state); err != nil {
		return MountState{}, err
	}
	return state, nil
}

func (r *Runtime) Delete(mountpoint string) error {
	err := os.Remove(r.statePath(mountpoint))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (r *Runtime) LogPath(mountpoint string) string {
	return filepath.Join(r.root, stateKey(mountpoint)+".log")
}

func (r *Runtime) ClearStale(mountpoint string) error {
	state, err := r.Load(mountpoint)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if ProcessExists(state.PID) {
		return nil
	}
	_ = os.Remove(r.LogPath(mountpoint))
	return r.Delete(mountpoint)
}

func (r *Runtime) WaitForMount(ctx context.Context, mountpoint string, pid int) (MountState, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := r.Load(mountpoint)
		switch {
		case err == nil:
			switch state.Status {
			case MountStatusMounted:
				return state, nil
			case MountStatusFailed:
				if state.Error == "" {
					return MountState{}, fmt.Errorf("mount helper %d failed for %s", state.PID, mountpoint)
				}
				return MountState{}, errors.New(state.Error)
			case MountStatusStarting:
				if state.PID > 0 && !ProcessExists(state.PID) {
					return MountState{}, fmt.Errorf("mount helper %d exited before mount became ready", state.PID)
				}
			}
		case errors.Is(err, os.ErrNotExist):
			if pid > 0 && !ProcessExists(pid) {
				return MountState{}, fmt.Errorf("mount helper %d exited before writing runtime state", pid)
			}
		default:
			return MountState{}, err
		}

		select {
		case <-ctx.Done():
			return MountState{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func ProcessExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (r *Runtime) statePath(mountpoint string) string {
	return filepath.Join(r.root, stateKey(mountpoint)+".json")
}

func stateKey(mountpoint string) string {
	sum := sha256.Sum256([]byte(mountpoint))
	return hex.EncodeToString(sum[:])
}
