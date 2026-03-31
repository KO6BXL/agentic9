package dp9ik

import (
	"encoding/binary"
	"testing"
)

func TestTicketReqRoundTrip(t *testing.T) {
	req := TicketReq{
		Type:    1,
		AuthID:  "bootes",
		AuthDom: "example.net",
		HostID:  "bootes",
		UID:     "glenda",
	}
	copy(req.Chal[:], []byte("12345678"))
	wire, err := req.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	var got TicketReq
	if err := got.UnmarshalBinary(wire); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if got.AuthID != req.AuthID || got.AuthDom != req.AuthDom || got.UID != req.UID {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

func TestParseTranscript(t *testing.T) {
	buf := make([]byte, 0, 12)
	frame := func(s string) []byte {
		tmp := make([]byte, 2+len(s))
		binary.BigEndian.PutUint16(tmp[:2], uint16(len(s)))
		copy(tmp[2:], s)
		return tmp
	}
	buf = append(buf, frame("one")...)
	buf = append(buf, frame("two")...)
	tr, err := ParseTranscript(buf)
	if err != nil {
		t.Fatalf("ParseTranscript: %v", err)
	}
	if len(tr.Messages) != 2 || string(tr.Messages[1]) != "two" {
		t.Fatalf("unexpected transcript: %#v", tr)
	}
}
