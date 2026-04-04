package dp9ik

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"agentic9/internal/config"

	"golang.org/x/crypto/hkdf"
)

func DialAuthServer(ctx context.Context, profile config.Profile) (net.Conn, error) {
	dialer := &net.Dialer{}
	return dialer.DialContext(ctx, "tcp", serviceAddress(profile.AuthHost, authPort))
}

func Authenticate(ctx context.Context, cpuConn net.Conn, profile config.Profile, secret string) (SessionKeys, error) {
	authConn, err := DialAuthServer(ctx, profile)
	if err != nil {
		return SessionKeys{}, err
	}
	defer authConn.Close()
	return authenticateWithAuthServer(ctx, cpuConn, authConn, profile, secret)
}

func authenticateWithAuthServer(ctx context.Context, cpuConn net.Conn, authConn net.Conn, profile config.Profile, secret string) (SessionKeys, error) {
	if err := applyDeadline(cpuConn, ctx); err != nil {
		return SessionKeys{}, err
	}
	if err := applyDeadline(authConn, ctx); err != nil {
		return SessionKeys{}, err
	}

	br := bufio.NewReader(cpuConn)
	offer, err := readCString(br)
	if err != nil {
		return SessionKeys{}, err
	}
	v2, domain, err := chooseDP9IKOffer(offer, profile.AuthDomain)
	if err != nil {
		return SessionKeys{}, err
	}
	if _, err := io.WriteString(cpuConn, "dp9ik "+domain+"\x00"); err != nil {
		return SessionKeys{}, err
	}
	if v2 {
		ok, err := readCString(br)
		if err != nil {
			return SessionKeys{}, err
		}
		if ok != "OK" {
			return SessionKeys{}, fmt.Errorf("unexpected p9any confirmation %q", ok)
		}
	}

	var clientChal [ChalLen]byte
	if err := randomBytes(clientChal[:]); err != nil {
		return SessionKeys{}, err
	}
	if _, err := cpuConn.Write(clientChal[:]); err != nil {
		return SessionKeys{}, err
	}

	trWire := make([]byte, TicketReqLen+PAKYLen)
	if _, err := io.ReadFull(br, trWire); err != nil {
		return SessionKeys{}, err
	}
	var tr TicketReq
	if err := tr.UnmarshalBinary(trWire[:TicketReqLen]); err != nil {
		return SessionKeys{}, err
	}
	if tr.Type != AuthPAK {
		return SessionKeys{}, fmt.Errorf("unexpected ticket request type %d", tr.Type)
	}
	serverY := append([]byte(nil), trWire[TicketReqLen:]...)

	clientKey := NewAuthKey(secret, profile.User)
	tr.HostID = profile.User
	tr.UID = profile.User
	serverYReply, err := requestPAKExchange(ctx, authConn, tr, serverY, &clientKey, profile.User)
	if err != nil {
		return SessionKeys{}, err
	}

	tickets, err := requestTickets(ctx, authConn, tr)
	if err != nil {
		return SessionKeys{}, err
	}
	clientTicketWire, serverTicketWire, err := splitTicketPair(tickets)
	if err != nil {
		return SessionKeys{}, err
	}

	var clientTicket Ticket
	if err := clientTicket.UnmarshalBinary(clientTicketWire, &clientKey); err != nil {
		return SessionKeys{}, ErrWrongSecret
	}
	if clientTicket.Num != AuthTc {
		return SessionKeys{}, ErrWrongSecret
	}

	if _, err := cpuConn.Write(serverYReply); err != nil {
		return SessionKeys{}, err
	}

	var clientRand [NonceLen]byte
	if err := randomBytes(clientRand[:]); err != nil {
		return SessionKeys{}, err
	}
	auth := Authenticator{Num: AuthAc, Rand: clientRand}
	copy(auth.Chal[:], tr.Chal[:])
	authWire, err := auth.MarshalBinary(&clientTicket)
	if err != nil {
		return SessionKeys{}, err
	}
	payload := make([]byte, 0, len(serverTicketWire)+len(authWire))
	payload = append(payload, serverTicketWire...)
	payload = append(payload, authWire...)
	if _, err := cpuConn.Write(payload); err != nil {
		return SessionKeys{}, err
	}

	serverAuthWire := make([]byte, AuthLen)
	if _, err := io.ReadFull(br, serverAuthWire); err != nil {
		return SessionKeys{}, err
	}
	var serverAuth Authenticator
	if err := serverAuth.UnmarshalBinary(serverAuthWire, &clientTicket); err != nil {
		return SessionKeys{}, err
	}
	if serverAuth.Num != AuthAs || serverAuth.Chal != clientChal {
		return SessionKeys{}, ErrInvalidChallenge
	}

	sessionSecret, err := deriveSessionSecret(clientTicket.Key[:], clientRand[:], serverAuth.Rand[:])
	if err != nil {
		return SessionKeys{}, err
	}
	return SessionKeys{
		AuthID:        tr.AuthID,
		AuthDomain:    tr.AuthDom,
		ClientUser:    clientTicket.CUID,
		ServerUser:    clientTicket.SUID,
		TicketKey:     clientTicket.Key,
		ClientRandom:  clientRand,
		ServerRandom:  serverAuth.Rand,
		SessionSecret: sessionSecret,
	}, nil
}

func chooseDP9IKOffer(offer, wantedDomain string) (bool, string, error) {
	v2 := false
	if strings.HasPrefix(offer, "v.2 ") {
		v2 = true
		offer = strings.TrimPrefix(offer, "v.2 ")
	}
	fields := strings.Fields(offer)
	var fallback string
	for _, field := range fields {
		proto, dom, ok := strings.Cut(field, "@")
		if !ok || proto != "dp9ik" {
			continue
		}
		if dom == wantedDomain {
			return v2, dom, nil
		}
		if fallback == "" {
			fallback = dom
		}
	}
	if fallback == "" {
		return false, "", ErrNoDP9IKOffer
	}
	return v2, fallback, nil
}

func readCString(r *bufio.Reader) (string, error) {
	s, err := r.ReadString(0)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(s, "\x00"), nil
}

func requestPAKExchange(ctx context.Context, authConn net.Conn, tr TicketReq, serverY []byte, clientKey *AuthKey, user string) ([]byte, error) {
	if len(serverY) != PAKYLen {
		return nil, errors.New("invalid server AuthPAK key")
	}
	tr.Type = AuthPAK
	req, err := tr.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if _, err := authConn.Write(req); err != nil {
		return nil, err
	}
	if _, err := authConn.Write(serverY); err != nil {
		return nil, err
	}

	pak, clientY, err := clientKey.NewPAK(rand.Reader, true)
	if err != nil {
		return nil, err
	}
	if _, err := authConn.Write(clientY); err != nil {
		return nil, err
	}

	reply, err := readAuthServerResponse(ctx, authConn, 2*PAKYLen)
	if err != nil {
		return nil, err
	}
	serverYReply := append([]byte(nil), reply[:PAKYLen]...)
	if err := clientKey.FinishPAK(&pak, reply[PAKYLen:]); err != nil {
		return nil, err
	}
	return serverYReply, nil
}

func requestTickets(ctx context.Context, authConn net.Conn, tr TicketReq) ([]byte, error) {
	tr.Type = AuthTreq
	req, err := tr.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if _, err := authConn.Write(req); err != nil {
		return nil, err
	}
	status := make([]byte, 1)
	if _, err := io.ReadFull(authConn, status); err != nil {
		return nil, err
	}
	switch status[0] {
	case AuthOK:
	case AuthErr:
		msg := make([]byte, 64)
		if _, err := io.ReadFull(authConn, msg); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("auth server: %s", strings.TrimRight(string(msg), "\x00"))
	default:
		return nil, fmt.Errorf("unexpected auth server response %d", status[0])
	}
	buf := make([]byte, 2*TicketLen)
	if _, err := io.ReadFull(authConn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func readAuthServerResponse(ctx context.Context, conn net.Conn, want int) ([]byte, error) {
	status := make([]byte, 1)
	if _, err := io.ReadFull(conn, status); err != nil {
		return nil, err
	}
	switch status[0] {
	case AuthOK:
		buf := make([]byte, want)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return nil, err
		}
		return buf, nil
	case AuthErr:
		msg := make([]byte, 64)
		if _, err := io.ReadFull(conn, msg); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("auth server: %s", strings.TrimRight(string(msg), "\x00"))
	case AuthOKvar:
		lenBuf := make([]byte, 5)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return nil, err
		}
		n := 0
		for _, b := range lenBuf {
			if b < '0' || b > '9' {
				return nil, errors.New("invalid variable auth response")
			}
			n = n*10 + int(b-'0')
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return nil, err
		}
		return buf, nil
	default:
		return nil, fmt.Errorf("unexpected auth server response %d", status[0])
	}
}

func splitTicketPair(buf []byte) ([]byte, []byte, error) {
	if len(buf) != 2*TicketLen {
		return nil, nil, fmt.Errorf("unexpected ticket pair length %d", len(buf))
	}
	return append([]byte(nil), buf[:TicketLen]...), append([]byte(nil), buf[TicketLen:]...), nil
}

func deriveSessionSecret(ticketKey, clientRand, serverRand []byte) ([]byte, error) {
	salt := make([]byte, 0, len(clientRand)+len(serverRand))
	salt = append(salt, clientRand...)
	salt = append(salt, serverRand...)
	reader := hkdf.New(sha256.New, ticketKey, salt, []byte("Plan 9 session secret"))
	out := make([]byte, 256)
	_, err := io.ReadFull(reader, out)
	return out, err
}

func applyDeadline(conn net.Conn, ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok {
		return conn.SetDeadline(deadline)
	}
	return nil
}

func serviceAddress(host, port string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, port)
}
