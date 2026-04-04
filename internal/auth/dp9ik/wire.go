package dp9ik

import (
	"crypto/rand"
	"errors"
	"fmt"
	"sync/atomic"

	"golang.org/x/crypto/chacha20poly1305"
)

var form1Counter atomic.Uint32

var form1TicketSignatures = map[byte]string{
	AuthTs: "form1 Ts",
	AuthTc: "form1 Tc",
}

var form1AuthSignatures = map[byte]string{
	AuthAs: "form1 As",
	AuthAc: "form1 Ac",
}

func form1Encode(num byte, plaintext []byte, key []byte) []byte {
	sig, ok := form1Signature(num)
	if !ok {
		panic(fmt.Sprintf("unknown form1 signature %d", num))
	}
	nonce := make([]byte, 12)
	copy(nonce[:8], sig)
	counter := form1Counter.Add(1) - 1
	nonce[8] = byte(counter)
	nonce[9] = byte(counter >> 8)
	nonce[10] = byte(counter >> 16)
	nonce[11] = byte(counter >> 24)
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		panic(err)
	}
	ct := aead.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 12+len(ct))
	copy(out, nonce)
	copy(out[12:], ct)
	return out
}

func form1Decode(data []byte, key []byte, allowed map[byte]string) (byte, []byte, error) {
	if len(data) < 12+16 {
		return 0, nil, errors.New("short form1 payload")
	}
	num, ok := form1Type(data[:8], allowed)
	if !ok {
		return 0, nil, errors.New("invalid form1 signature")
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return 0, nil, err
	}
	pt, err := aead.Open(nil, data[:12], data[12:], nil)
	if err != nil {
		return 0, nil, err
	}
	return num, pt, nil
}

func form1Signature(num byte) ([]byte, bool) {
	switch num {
	case AuthTs:
		return []byte("form1 Ts"), true
	case AuthTc:
		return []byte("form1 Tc"), true
	case AuthAs:
		return []byte("form1 As"), true
	case AuthAc:
		return []byte("form1 Ac"), true
	default:
		return nil, false
	}
}

func form1Type(sig []byte, allowed map[byte]string) (byte, bool) {
	for num, want := range allowed {
		if string(sig) == want {
			return num, true
		}
	}
	return 0, false
}

func randomBytes(dst []byte) error {
	_, err := rand.Read(dst)
	return err
}
