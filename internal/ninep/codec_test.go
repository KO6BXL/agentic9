package ninep

import "testing"

func TestMarshalUnmarshalRead(t *testing.T) {
	in := Fcall{Type: RREAD, Tag: 7, Data: []byte("hello")}
	wire, err := Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out, err := Unmarshal(wire)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Type != in.Type || out.Tag != in.Tag || string(out.Data) != "hello" {
		t.Fatalf("unexpected round trip: %#v", out)
	}
}

func TestMarshalUnmarshalStat(t *testing.T) {
	in := Fcall{Type: RSTAT, Tag: 1, Dir: &Dir{Name: "hello", UID: "glenda", GID: "glenda", MUID: "glenda"}}
	wire, err := Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out, err := Unmarshal(wire)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Dir == nil || out.Dir.Name != "hello" {
		t.Fatalf("unexpected dir: %#v", out.Dir)
	}
}
