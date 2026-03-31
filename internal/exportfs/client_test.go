package exportfs

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"agentic9/internal/ninep"
)

type fakeOpener struct {
	stream io.ReadWriteCloser
}

func (f fakeOpener) OpenExportFS(context.Context, string) (io.ReadWriteCloser, error) {
	return f.stream, nil
}

type fakeStream struct {
	t            *testing.T
	expectations []expectation
	readBuf      bytes.Buffer
}

type expectation struct {
	check func(ninep.Fcall)
	reply ninep.Fcall
}

func (s *fakeStream) Write(p []byte) (int, error) {
	req, err := ninep.Unmarshal(p)
	if err != nil {
		s.t.Fatalf("unmarshal request: %v", err)
	}
	if len(s.expectations) == 0 {
		s.t.Fatalf("unexpected request: %#v", req)
	}
	exp := s.expectations[0]
	s.expectations = s.expectations[1:]
	if exp.check != nil {
		exp.check(req)
	}
	reply := exp.reply
	reply.Tag = req.Tag
	wire, err := ninep.Marshal(reply)
	if err != nil {
		s.t.Fatalf("marshal reply: %v", err)
	}
	if _, err := s.readBuf.Write(wire); err != nil {
		s.t.Fatalf("buffer reply: %v", err)
	}
	return len(p), nil
}

func (s *fakeStream) Read(p []byte) (int, error) {
	return s.readBuf.Read(p)
}

func (s *fakeStream) Close() error { return nil }

func TestClientListDecodesDirectoryEntriesAndUsesDeterministicFIDs(t *testing.T) {
	rootQID := ninep.QID{Type: 0x80, Path: 1}
	dirData := mustDirData(t,
		&ninep.Dir{Name: "dir", UID: "glenda", GID: "glenda", MUID: "glenda", Mode: 0x80000000 | 0o755},
		&ninep.Dir{Name: "file.txt", UID: "glenda", GID: "glenda", MUID: "glenda", Mode: 0o644, Length: 12},
	)
	stream := &fakeStream{t: t, expectations: []expectation{
		{
			check: func(req ninep.Fcall) {
				if req.Type != ninep.TVERSION || req.Version != "9P2000" {
					t.Fatalf("unexpected version request: %#v", req)
				}
			},
			reply: ninep.Fcall{Type: ninep.RVERSION, Msize: 8192, Version: "9P2000"},
		},
		{
			check: func(req ninep.Fcall) {
				if req.Type != ninep.TATTACH || req.FID != 1 {
					t.Fatalf("unexpected attach request: %#v", req)
				}
			},
			reply: ninep.Fcall{Type: ninep.RATTACH, QID: rootQID},
		},
		{
			check: func(req ninep.Fcall) {
				if req.Type != ninep.TWALK || req.FID != 1 || req.NewFID != 2 || len(req.WNames) != 0 {
					t.Fatalf("unexpected walk-to-root request: %#v", req)
				}
			},
			reply: ninep.Fcall{Type: ninep.RWALK, WQIDs: nil},
		},
		{
			check: func(req ninep.Fcall) {
				if req.Type != ninep.TSTAT || req.FID != 2 {
					t.Fatalf("unexpected stat request: %#v", req)
				}
			},
			reply: ninep.Fcall{Type: ninep.RSTAT, Dir: &ninep.Dir{Name: "/", UID: "glenda", GID: "glenda", MUID: "glenda", Mode: 0x80000000 | 0o755}},
		},
		{
			check: func(req ninep.Fcall) {
				if req.Type != ninep.TOPEN || req.FID != 2 || req.Mode != 0 {
					t.Fatalf("unexpected open request: %#v", req)
				}
			},
			reply: ninep.Fcall{Type: ninep.ROPEN, QID: rootQID},
		},
		{
			check: func(req ninep.Fcall) {
				if req.Type != ninep.TREAD || req.FID != 2 || req.Offset != 0 {
					t.Fatalf("unexpected first read request: %#v", req)
				}
			},
			reply: ninep.Fcall{Type: ninep.RREAD, Data: dirData},
		},
		{
			check: func(req ninep.Fcall) {
				if req.Type != ninep.TREAD || req.FID != 2 || req.Offset != uint64(len(dirData)) {
					t.Fatalf("unexpected second read request: %#v", req)
				}
			},
			reply: ninep.Fcall{Type: ninep.RREAD, Data: nil},
		},
		{
			check: func(req ninep.Fcall) {
				if req.Type != ninep.TCLUNK || req.FID != 2 {
					t.Fatalf("unexpected first clunk request: %#v", req)
				}
			},
			reply: ninep.Fcall{Type: ninep.RCLUNK},
		},
		{
			check: func(req ninep.Fcall) {
				if req.Type != ninep.TWALK || req.FID != 1 || req.NewFID != 3 || len(req.WNames) != 1 || req.WNames[0] != "dir" {
					t.Fatalf("unexpected walk-to-child request: %#v", req)
				}
			},
			reply: ninep.Fcall{Type: ninep.RWALK, WQIDs: []ninep.QID{{Type: 0x80, Path: 2}}},
		},
		{
			check: func(req ninep.Fcall) {
				if req.Type != ninep.TSTAT || req.FID != 3 {
					t.Fatalf("unexpected child stat request: %#v", req)
				}
			},
			reply: ninep.Fcall{Type: ninep.RSTAT, Dir: &ninep.Dir{Name: "dir", UID: "glenda", GID: "glenda", MUID: "glenda", Mode: 0x80000000 | 0o755}},
		},
		{
			check: func(req ninep.Fcall) {
				if req.Type != ninep.TCLUNK || req.FID != 3 {
					t.Fatalf("unexpected second clunk request: %#v", req)
				}
			},
			reply: ninep.Fcall{Type: ninep.RCLUNK},
		},
	}}
	client := New(fakeOpener{stream: stream}, "/remote")

	entries, err := client.List(context.Background(), "/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %#v", entries)
	}
	if entries[0].Path != "/dir" || entries[0].Mode&os.ModeDir == 0 {
		t.Fatalf("unexpected first entry: %#v", entries[0])
	}
	if entries[1].Path != "/file.txt" || entries[1].Mode.Perm() != 0o644 || entries[1].Size != 12 {
		t.Fatalf("unexpected second entry: %#v", entries[1])
	}

	if _, err := client.Stat(context.Background(), "/dir"); err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if len(stream.expectations) != 0 {
		t.Fatalf("unconsumed expectations: %d", len(stream.expectations))
	}
}

func mustDirData(t *testing.T, dirs ...*ninep.Dir) []byte {
	t.Helper()
	var data []byte
	for _, dir := range dirs {
		encoded, err := dirWire(dir)
		if err != nil {
			t.Fatalf("encode dir %q: %v", dir.Name, err)
		}
		data = append(data, encoded...)
	}
	return data
}

func dirWire(dir *ninep.Dir) ([]byte, error) {
	return ninep.EncodeDir(dir)
}
