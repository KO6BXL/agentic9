package dp9ik

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	ANameLen = 28
	DomLen   = 48
	ChalLen  = 8

	NonceLen  = 32
	AESKeyLen = 16

	PAKKeyLen  = 32
	PAKSLen    = (448 + 7) / 8
	PAKPLen    = 4 * PAKSLen
	PAKHashLen = 2 * PAKPLen
	PAKXLen    = PAKSLen
	PAKYLen    = PAKSLen

	TicketReqLen = 3*ANameLen + ChalLen + DomLen + 1
	TicketLen    = 12 + ChalLen + 2*ANameLen + NonceLen + 16
	AuthLen      = 12 + ChalLen + NonceLen + 16
)

const (
	AuthTreq  byte = 1
	AuthOK    byte = 4
	AuthErr   byte = 5
	AuthOKvar byte = 9
	AuthPAK   byte = 19

	AuthTs byte = 64
	AuthTc byte = 65
	AuthAs byte = 66
	AuthAc byte = 67
)

const (
	authPort = "567"
)

var (
	ErrNoDP9IKOffer     = errors.New("server did not offer dp9ik")
	ErrInvalidChallenge = errors.New("server authenticator challenge mismatch")
	ErrWrongSecret      = errors.New("authentication secret was rejected")
)

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

type Authenticator struct {
	Num  byte
	Chal [ChalLen]byte
	Rand [NonceLen]byte
}

type Transcript struct {
	Messages [][]byte
}

type AuthKey struct {
	AES     [AESKeyLen]byte
	PakKey  [PAKKeyLen]byte
	PakHash [PAKHashLen]byte
}

type PAKState struct {
	isClient bool
	x        [PAKXLen]byte
	y        [PAKYLen]byte
}

type SessionKeys struct {
	AuthID        string
	AuthDomain    string
	ClientUser    string
	ServerUser    string
	TicketKey     [NonceLen]byte
	ClientRandom  [NonceLen]byte
	ServerRandom  [NonceLen]byte
	SessionSecret []byte
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

func (t Ticket) MarshalBinary(key *AuthKey) ([]byte, error) {
	if key == nil {
		return nil, errors.New("ticket key is required")
	}
	payload := make([]byte, 1+ChalLen+2*ANameLen+NonceLen)
	p := payload
	p[0] = t.Num
	copy(p[1:1+ChalLen], t.Chal[:])
	putFixed(p[1+ChalLen:1+ChalLen+ANameLen], t.CUID)
	putFixed(p[1+ChalLen+ANameLen:1+ChalLen+2*ANameLen], t.SUID)
	copy(p[1+ChalLen+2*ANameLen:], t.Key[:])
	return form1Encode(t.Num, payload[1:], key.PakKey[:]), nil
}

func (t *Ticket) UnmarshalBinary(data []byte, key *AuthKey) error {
	if key == nil {
		return errors.New("ticket key is required")
	}
	num, payload, err := form1Decode(data, key.PakKey[:], form1TicketSignatures)
	if err != nil {
		return err
	}
	if len(payload) != ChalLen+2*ANameLen+NonceLen {
		return fmt.Errorf("ticket payload length %d", len(payload))
	}
	t.Num = num
	copy(t.Chal[:], payload[:ChalLen])
	t.CUID = trimFixed(payload[ChalLen : ChalLen+ANameLen])
	t.SUID = trimFixed(payload[ChalLen+ANameLen : ChalLen+2*ANameLen])
	copy(t.Key[:], payload[ChalLen+2*ANameLen:])
	t.Form = 1
	return nil
}

func (a Authenticator) MarshalBinary(ticket *Ticket) ([]byte, error) {
	if ticket == nil {
		return nil, errors.New("ticket is required")
	}
	payload := make([]byte, 1+ChalLen+NonceLen)
	payload[0] = a.Num
	copy(payload[1:1+ChalLen], a.Chal[:])
	copy(payload[1+ChalLen:], a.Rand[:])
	return form1Encode(a.Num, payload[1:], ticket.Key[:]), nil
}

func (a *Authenticator) UnmarshalBinary(data []byte, ticket *Ticket) error {
	if ticket == nil {
		return errors.New("ticket is required")
	}
	num, payload, err := form1Decode(data, ticket.Key[:], form1AuthSignatures)
	if err != nil {
		return err
	}
	if len(payload) != ChalLen+NonceLen {
		return fmt.Errorf("authenticator payload length %d", len(payload))
	}
	a.Num = num
	copy(a.Chal[:], payload[:ChalLen])
	copy(a.Rand[:], payload[ChalLen:])
	return nil
}

func NewAuthKey(secret, user string) AuthKey {
	var key AuthKey
	passtoaeskey(key.AES[:], secret)
	authpakHash(&key, user)
	return key
}

func (k *AuthKey) NewPAK(randReader io.Reader, isClient bool) (PAKState, []byte, error) {
	return authpakNew(randReader, k, isClient)
}

func (k *AuthKey) FinishPAK(state *PAKState, peer []byte) error {
	return authpakFinish(state, k, peer)
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
