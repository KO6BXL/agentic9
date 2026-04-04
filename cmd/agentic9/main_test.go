package main

import "testing"

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
