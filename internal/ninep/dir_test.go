package ninep

import "testing"

func TestParseDirEntriesRoundTrip(t *testing.T) {
	a, err := marshalDir(&Dir{Name: "alpha", UID: "glenda", GID: "glenda", MUID: "glenda"})
	if err != nil {
		t.Fatalf("marshalDir alpha: %v", err)
	}
	b, err := marshalDir(&Dir{Name: "beta", UID: "glenda", GID: "glenda", MUID: "glenda"})
	if err != nil {
		t.Fatalf("marshalDir beta: %v", err)
	}
	dirs, err := ParseDirEntries(append(a, b...))
	if err != nil {
		t.Fatalf("ParseDirEntries: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 dirs, got %d", len(dirs))
	}
	if dirs[0].Name != "alpha" || dirs[1].Name != "beta" {
		t.Fatalf("unexpected dirs: %#v", dirs)
	}
}

func TestParseDirEntriesRejectsTruncatedEntry(t *testing.T) {
	buf, err := marshalDir(&Dir{Name: "alpha", UID: "glenda", GID: "glenda", MUID: "glenda"})
	if err != nil {
		t.Fatalf("marshalDir: %v", err)
	}
	_, err = ParseDirEntries(buf[:len(buf)-1])
	if err == nil {
		t.Fatal("expected error for truncated dir entry")
	}
}
