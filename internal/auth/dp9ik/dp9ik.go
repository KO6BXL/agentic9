package dp9ik

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	ANameLen     = 28
	DomLen       = 48
	ChalLen      = 8
	NonceLen     = 32
	TicketReqLen = 3*ANameLen + ChalLen + DomLen + 1
)

var ErrUnimplemented = errors.New("dp9ik transport is not implemented yet")

type TicketReq struct {
	Type    byte
	AuthID  string
	AuthDom string
	Chal    [ChalLen]byte
	HostID  string
	UID     string
}

type Ticket struct {
	Num  byte
	Chal [ChalLen]byte
	CUID string
	SUID string
	Key  [NonceLen]byte
	Form byte
}

type Transcript struct {
	Messages [][]byte
}

func (t TicketReq) MarshalBinary() ([]byte, error) {
	buf := make([]byte, TicketReqLen)
	buf[0] = t.Type
	putFixed(buf[1:1+ANameLen], t.AuthID)
	putFixed(buf[1+ANameLen:1+ANameLen+DomLen], t.AuthDom)
	copy(buf[1+ANameLen+DomLen:1+ANameLen+DomLen+ChalLen], t.Chal[:])
	putFixed(buf[1+ANameLen+DomLen+ChalLen:1+2*ANameLen+DomLen+ChalLen], t.HostID)
	putFixed(buf[1+2*ANameLen+DomLen+ChalLen:], t.UID)
	return buf, nil
}

func (t *TicketReq) UnmarshalBinary(data []byte) error {
	if len(data) != TicketReqLen {
		return fmt.Errorf("ticketreq length %d", len(data))
	}
	t.Type = data[0]
	t.AuthID = trimFixed(data[1 : 1+ANameLen])
	t.AuthDom = trimFixed(data[1+ANameLen : 1+ANameLen+DomLen])
	copy(t.Chal[:], data[1+ANameLen+DomLen:1+ANameLen+DomLen+ChalLen])
	t.HostID = trimFixed(data[1+ANameLen+DomLen+ChalLen : 1+2*ANameLen+DomLen+ChalLen])
	t.UID = trimFixed(data[1+2*ANameLen+DomLen+ChalLen:])
	return nil
}

func ParseTranscript(frame []byte) (Transcript, error) {
	var out Transcript
	for len(frame) > 0 {
		if len(frame) < 2 {
			return Transcript{}, errors.New("short frame header")
		}
		n := int(binary.BigEndian.Uint16(frame[:2]))
		frame = frame[2:]
		if len(frame) < n {
			return Transcript{}, errors.New("short frame payload")
		}
		msg := make([]byte, n)
		copy(msg, frame[:n])
		out.Messages = append(out.Messages, msg)
		frame = frame[n:]
	}
	return out, nil
}

func putFixed(dst []byte, value string) {
	copy(dst, []byte(value))
}

func trimFixed(src []byte) string {
	n := len(src)
	for n > 0 && src[n-1] == 0 {
		n--
	}
	return string(src[:n])
}
