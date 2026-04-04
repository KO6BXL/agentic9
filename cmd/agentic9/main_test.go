package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
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
	if resp["edit_path"] != "/mnt/a9" {
		t.Fatalf("edit_path = %#v, want /mnt/a9", resp["edit_path"])
	}
	if resp["source_path"] != "/src" {
		t.Fatalf("source_path = %#v, want /src", resp["source_path"])
	}
	if resp["sync_mode"] != "copy-once" {
		t.Fatalf("sync_mode = %#v, want copy-once", resp["sync_mode"])
	}

	resp = workspaceCreateJSON("agent-1", "/remote/root", "/mnt/a9", "/src", 42, true)
	if resp["sync_mode"] != "mirror-once" {
		t.Fatalf("sync_mode = %#v, want mirror-once", resp["sync_mode"])
	}
}
