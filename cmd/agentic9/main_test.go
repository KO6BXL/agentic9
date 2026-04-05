package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"syscall"
	"testing"
	"time"

	"agentic9/internal/buildinfo"
	"agentic9/internal/config"
	"agentic9/internal/fusefs"
	"agentic9/internal/workspace"
)

func TestWorkspaceDeleteReportFinish(t *testing.T) {
	report := workspaceDeleteReport{
		OK:             false,
		MetadataLookup: okStep(),
		Unmount:        errorStep(assertErr("umount failed")),
		RemoteDelete:   errorStep(assertErr("remote delete failed")),
		Metadata:       skippedStep("left in place"),
	}
	report.finish()
	if report.Error != "unmount: umount failed; remote delete: remote delete failed" {
		t.Fatalf("unexpected error summary: %q", report.Error)
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

func assertErr(msg string) error { return testErr(msg) }

func TestExecOutputRouterSuppressesKnownBenignWarnings(t *testing.T) {
	r := newExecOutputRouter(false)
	oldStdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	os.Stdout = writeEnd
	defer func() {
		os.Stdout = oldStdout
	}()

	input := []byte(
		"bind: /mnt/term/dev/cons: file does not exist: '/mnt/term/dev'\n" +
			"ok\n" +
			"bind: /mnt/term/dev: file does not exist: '/mnt/term/dev'\n",
	)
	if err := r.Write(input); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := writeEnd.Close(); err != nil {
		t.Fatalf("Close write end: %v", err)
	}

	var got bytes.Buffer
	if _, err := got.ReadFrom(readEnd); err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if got.String() != "ok\n" {
		t.Fatalf("unexpected stdout: %q", got.String())
	}
	if len(r.warnings) != 2 {
		t.Fatalf("warnings = %d, want 2", len(r.warnings))
	}
	for _, warning := range r.warnings {
		if !warning.Benign || warning.Kind != "benign_remote_bind_warning" {
			t.Fatalf("unexpected warning: %#v", warning)
		}
	}
}

func TestExecOutputRouterFlushesPartialLineInJSONMode(t *testing.T) {
	r := newExecOutputRouter(true)
	oldStdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	os.Stdout = writeEnd
	defer func() {
		os.Stdout = oldStdout
	}()

	if err := r.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := writeEnd.Close(); err != nil {
		t.Fatalf("Close write end: %v", err)
	}

	var out bytes.Buffer
	if _, err := out.ReadFrom(readEnd); err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	var event struct {
		Type   string `json:"type"`
		Stream string `json:"stream"`
		Data   string `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &event); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if event.Type != "output" || event.Stream != "remote" || event.Data != "hello" {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestWorkspaceCreateJSONIncludesEditPathAndSyncMode(t *testing.T) {
	resp := workspaceCreateJSON("agent-1", "/remote/root", "/mnt/a9", "/src", 42, false)
	if resp["project_root"] != "/mnt/a9" {
		t.Fatalf("project_root = %#v, want /mnt/a9", resp["project_root"])
	}
	if resp["cli_version"] != buildinfo.CLIVersion {
		t.Fatalf("cli_version = %#v, want %q", resp["cli_version"], buildinfo.CLIVersion)
	}
	if resp["expected_skill_version"] != buildinfo.SkillVersion {
		t.Fatalf("expected_skill_version = %#v, want %q", resp["expected_skill_version"], buildinfo.SkillVersion)
	}
	if resp["edit_path"] != "/mnt/a9" {
		t.Fatalf("edit_path = %#v, want /mnt/a9", resp["edit_path"])
	}
	if resp["remote_project_root"] != "/remote/root" {
		t.Fatalf("remote_project_root = %#v, want /remote/root", resp["remote_project_root"])
	}
	if resp["seed_path"] != "/src" {
		t.Fatalf("seed_path = %#v, want /src", resp["seed_path"])
	}
	if resp["source_path"] != "/src" {
		t.Fatalf("source_path = %#v, want /src", resp["source_path"])
	}
	if resp["bootstrap_mode"] != "copy-once" {
		t.Fatalf("bootstrap_mode = %#v, want copy-once", resp["bootstrap_mode"])
	}
	if resp["sync_mode"] != "copy-once" {
		t.Fatalf("sync_mode = %#v, want copy-once", resp["sync_mode"])
	}

	resp = workspaceCreateJSON("agent-1", "/remote/root", "/mnt/a9", "/src", 42, true)
	if resp["bootstrap_mode"] != "mirror-once" {
		t.Fatalf("bootstrap_mode = %#v, want mirror-once", resp["bootstrap_mode"])
	}
	if resp["sync_mode"] != "mirror-once" {
		t.Fatalf("sync_mode = %#v, want mirror-once", resp["sync_mode"])
	}
}

func TestResolveAliasedPathFlag(t *testing.T) {
	got, err := resolveAliasedPathFlag("--source", "/src", "--seed-path", "")
	if err != nil {
		t.Fatalf("resolveAliasedPathFlag primary only: %v", err)
	}
	if got != "/src" {
		t.Fatalf("primary only = %q, want /src", got)
	}

	got, err = resolveAliasedPathFlag("--source", "", "--seed-path", "/seed")
	if err != nil {
		t.Fatalf("resolveAliasedPathFlag alias only: %v", err)
	}
	if got != "/seed" {
		t.Fatalf("alias only = %q, want /seed", got)
	}

	got, err = resolveAliasedPathFlag("--source", "/same", "--seed-path", "/same")
	if err != nil {
		t.Fatalf("resolveAliasedPathFlag matching values: %v", err)
	}
	if got != "/same" {
		t.Fatalf("matching values = %q, want /same", got)
	}

	_, err = resolveAliasedPathFlag("--source", "/src", "--seed-path", "/other")
	if err == nil {
		t.Fatal("resolveAliasedPathFlag conflict unexpectedly succeeded")
	}
	if err.Error() != "--source and --seed-path must match when both are set" {
		t.Fatalf("unexpected conflict error: %v", err)
	}
}

func TestLoadWorkspaceMetadataRecoversFromRuntimeState(t *testing.T) {
	stateRoot := t.TempDir()
	runtimeRoot := t.TempDir()
	manager, err := workspace.NewManager(stateRoot)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	runtime := fusefs.NewRuntime(runtimeRoot)
	mountState := fusefs.MountState{
		Profile:    "default",
		AgentID:    "agent-123",
		Mountpoint: "/tmp/agentic9/default/agent-123",
		PID:        os.Getpid(),
		Status:     fusefs.MountStatusMounted,
		UpdatedAt:  time.Unix(1700000000, 0).UTC(),
	}
	if err := runtime.Save(mountState); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}

	profile := config.Profile{RemoteBase: "/usr/glenda/agentic9/workspaces"}
	meta, err := loadWorkspaceMetadata(runtimeRoot, manager, profile, "default", "agent-123")
	if err != nil {
		t.Fatalf("loadWorkspaceMetadata: %v", err)
	}
	if meta.Mountpoint != mountState.Mountpoint {
		t.Fatalf("Mountpoint = %q, want %q", meta.Mountpoint, mountState.Mountpoint)
	}
	if meta.RemoteRoot != "/usr/glenda/agentic9/workspaces/agent-123/root" {
		t.Fatalf("RemoteRoot = %q", meta.RemoteRoot)
	}
	if !meta.Mounted || meta.MountPID != os.Getpid() {
		t.Fatalf("unexpected recovered metadata: %#v", meta)
	}

	saved, err := manager.Load("default", "agent-123")
	if err != nil {
		t.Fatalf("manager.Load after recovery: %v", err)
	}
	if saved.Mountpoint != mountState.Mountpoint {
		t.Fatalf("saved Mountpoint = %q, want %q", saved.Mountpoint, mountState.Mountpoint)
	}
}

func TestRefreshWorkspaceMetadataRecoversMovedRuntimeState(t *testing.T) {
	stateRoot := t.TempDir()
	runtimeRoot := t.TempDir()
	manager, err := workspace.NewManager(stateRoot)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	meta := workspace.Metadata{
		Profile:    "default",
		AgentID:    "agent-123",
		RemoteRoot: "/usr/glenda/agentic9/workspaces/agent-123/root",
		Mountpoint: "/tmp/agentic9/default/old-agent-123",
		Mounted:    true,
		MountPID:   os.Getpid() + 100000,
		CreatedAt:  time.Unix(1700000000, 0).UTC(),
	}
	if err := manager.Save(meta); err != nil {
		t.Fatalf("manager.Save: %v", err)
	}

	runtime := fusefs.NewRuntime(runtimeRoot)
	mountState := fusefs.MountState{
		Profile:    "default",
		AgentID:    "agent-123",
		Mountpoint: "/tmp/agentic9/default/agent-123",
		PID:        os.Getpid(),
		Status:     fusefs.MountStatusMounted,
		UpdatedAt:  time.Unix(1700000100, 0).UTC(),
	}
	if err := runtime.Save(mountState); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}

	got, err := refreshWorkspaceMetadata(runtimeRoot, manager, meta)
	if err != nil {
		t.Fatalf("refreshWorkspaceMetadata: %v", err)
	}
	if !got.Mounted || got.Mountpoint != mountState.Mountpoint || got.MountPID != mountState.PID {
		t.Fatalf("unexpected refreshed metadata: %#v", got)
	}

	saved, err := manager.Load("default", "agent-123")
	if err != nil {
		t.Fatalf("manager.Load: %v", err)
	}
	if saved.Mountpoint != mountState.Mountpoint || saved.MountPID != mountState.PID {
		t.Fatalf("unexpected saved metadata: %#v", saved)
	}
}

func TestInspectWorkspaceStateFlagsMissingMetadataWithActiveRuntime(t *testing.T) {
	stateRoot := t.TempDir()
	runtimeRoot := t.TempDir()
	manager, err := workspace.NewManager(stateRoot)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	runtime := fusefs.NewRuntime(runtimeRoot)
	mountState := fusefs.MountState{
		Profile:    "default",
		AgentID:    "agent-123",
		Mountpoint: "/tmp/agentic9/default/agent-123",
		PID:        os.Getpid(),
		Status:     fusefs.MountStatusMounted,
		UpdatedAt:  time.Unix(1700000000, 0).UTC(),
	}
	if err := runtime.Save(mountState); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}

	report, err := inspectWorkspaceState(runtimeRoot, manager, config.Profile{RemoteBase: "/usr/glenda/agentic9/workspaces"}, "default", "agent-123")
	if err != nil {
		t.Fatalf("inspectWorkspaceState: %v", err)
	}
	if !report.Mounted || report.ProjectRoot != mountState.Mountpoint {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.Metadata.Present {
		t.Fatalf("metadata unexpectedly present: %#v", report.Metadata)
	}
	if !report.Runtime.Present || !report.Runtime.ProcessAlive {
		t.Fatalf("runtime not reported as active: %#v", report.Runtime)
	}
	if len(report.Inconsistencies) != 1 || report.Inconsistencies[0] != "metadata is missing while an active runtime mount exists" {
		t.Fatalf("unexpected inconsistencies: %#v", report.Inconsistencies)
	}
}

func TestVersionInfoMatchesBuildInfo(t *testing.T) {
	info := versionInfo()
	if info.CLIVersion != buildinfo.CLIVersion {
		t.Fatalf("CLIVersion = %q, want %q", info.CLIVersion, buildinfo.CLIVersion)
	}
	if info.ExpectedSkillVersion != buildinfo.SkillVersion {
		t.Fatalf("ExpectedSkillVersion = %q, want %q", info.ExpectedSkillVersion, buildinfo.SkillVersion)
	}
}

func TestDirectoryIsEmpty(t *testing.T) {
	dir := t.TempDir()
	empty, err := directoryIsEmpty(dir)
	if err != nil {
		t.Fatalf("directoryIsEmpty empty dir: %v", err)
	}
	if !empty {
		t.Fatal("directoryIsEmpty(empty) = false, want true")
	}

	if err := os.WriteFile(dir+"/file.txt", []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	empty, err = directoryIsEmpty(dir)
	if err != nil {
		t.Fatalf("directoryIsEmpty non-empty dir: %v", err)
	}
	if empty {
		t.Fatal("directoryIsEmpty(non-empty) = true, want false")
	}
}

func TestWaitForWritableMountOnExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForWritableMount(ctx, dir); err != nil {
		t.Fatalf("waitForWritableMount: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected entries left behind: %#v", entries)
	}
}

func TestIsRetriableMountReadyError(t *testing.T) {
	if !isRetriableMountReadyError(os.ErrNotExist) {
		t.Fatal("os.ErrNotExist should be retriable for mount readiness")
	}
	if !isRetriableMountReadyError(syscall.EIO) {
		t.Fatal("syscall.EIO should be retriable for mount readiness")
	}
	if isRetriableMountReadyError(assertErr("boom")) {
		t.Fatal("unexpected non-retriable error classified as retriable")
	}
}
