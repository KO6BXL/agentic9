package tlsrcpu

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agentic9/internal/auth/dp9ik"
	"agentic9/internal/config"
	"agentic9/internal/exportfs"
	"agentic9/internal/ninep"

	tlspsk "github.com/jc-lab/go-tls-psk"
	"golang.org/x/crypto/hkdf"
)

func TestVerifyAndExec(t *testing.T) {
	svc := newFakeService(t, "glenda", "example.net", "local")
	t.Cleanup(svc.Close)

	client := NewClient(svc.profile(), config.Secret{Value: "local", Source: "test"})
	if err := client.Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	var chunks [][]byte
	svc.execHandler = func(script string, conn net.Conn) {
		if !strings.Contains(script, "echo hello") {
			t.Errorf("script did not contain command: %q", script)
		}
		_, _ = conn.Write([]byte("hello\n"))
		_ = conn.Close()
	}
	if err := client.Exec(context.Background(), "echo hello\n", func(chunk []byte) error {
		chunks = append(chunks, append([]byte(nil), chunk...))
		return nil
	}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got := string(bytesJoin(chunks)); got != "hello\n" {
		t.Fatalf("unexpected output %q", got)
	}
}

func TestExecPropagatesCallbackError(t *testing.T) {
	svc := newFakeService(t, "glenda", "example.net", "local")
	t.Cleanup(svc.Close)
	svc.execHandler = func(script string, conn net.Conn) {
		_, _ = conn.Write([]byte("chunk-1"))
		time.Sleep(10 * time.Millisecond)
		_, _ = conn.Write([]byte("chunk-2"))
	}

	client := NewClient(svc.profile(), config.Secret{Value: "local", Source: "test"})
	want := errors.New("stop")
	err := client.Exec(context.Background(), "echo hello\n", func(chunk []byte) error {
		if string(chunk) == "chunk-1" {
			return want
		}
		return nil
	})
	if !errors.Is(err, want) {
		t.Fatalf("Exec error = %v, want %v", err, want)
	}
}

func TestOpenExportFS(t *testing.T) {
	svc := newFakeService(t, "glenda", "example.net", "local")
	t.Cleanup(svc.Close)
	svc.exportHandler = func(script string, conn net.Conn) {
		if !strings.Contains(script, "exportfs -r '/remote/root'") {
			t.Errorf("unexpected exportfs script %q", script)
		}
		serveNineP(t, conn)
	}

	client := NewClient(svc.profile(), config.Secret{Value: "local", Source: "test"})
	fs := exportfs.New(client, "/remote/root")
	entry, err := fs.Stat(context.Background(), "/hello.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if entry.Path != "/hello.txt" || entry.Size != 5 {
		t.Fatalf("unexpected entry: %#v", entry)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	svc := newFakeService(t, "glenda", "example.net", "local")
	t.Cleanup(svc.Close)
	client := NewClient(svc.profile(), config.Secret{Value: "wrong", Source: "test"})
	if err := client.Verify(context.Background()); !errors.Is(err, dp9ik.ErrWrongSecret) {
		t.Fatalf("Verify error = %v, want %v", err, dp9ik.ErrWrongSecret)
	}
}

type fakeService struct {
	t      *testing.T
	user   string
	domain string
	secret string

	authLn net.Listener
	cpuLn  net.Listener

	wg sync.WaitGroup

	execHandler   func(string, net.Conn)
	exportHandler func(string, net.Conn)
}

func newFakeService(t *testing.T, user, domain, secret string) *fakeService {
	t.Helper()
	authLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cpuLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeService{
		t:      t,
		user:   user,
		domain: domain,
		secret: secret,
		authLn: authLn,
		cpuLn:  cpuLn,
	}
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := authLn.Accept()
			if err != nil {
				return
			}
			go s.handleAuth(conn)
		}
	}()
	go func() {
		defer s.wg.Done()
		for {
			conn, err := cpuLn.Accept()
			if err != nil {
				return
			}
			go s.handleCPU(conn)
		}
	}()
	return s
}

func (s *fakeService) profile() config.Profile {
	return config.Profile{
		CPUHost:    s.cpuLn.Addr().String(),
		AuthHost:   s.authLn.Addr().String(),
		User:       s.user,
		AuthDomain: s.domain,
	}
}

func (s *fakeService) Close() {
	_ = s.authLn.Close()
	_ = s.cpuLn.Close()
	s.wg.Wait()
}

func (s *fakeService) handleAuth(conn net.Conn) {
	defer conn.Close()

	var clientKey dp9ik.AuthKey
	var serverKey dp9ik.AuthKey
	for {
		buf := make([]byte, dp9ik.TicketReqLen)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		var tr dp9ik.TicketReq
		if err := tr.UnmarshalBinary(buf); err != nil {
			s.t.Errorf("auth tr: %v", err)
			return
		}
		switch tr.Type {
		case dp9ik.AuthPAK:
			clientKey = dp9ik.NewAuthKey(s.secret, tr.HostID)
			serverKey = dp9ik.NewAuthKey(s.secret, tr.AuthID)

			yAs := make([]byte, dp9ik.PAKYLen)
			yAc := make([]byte, dp9ik.PAKYLen)
			if _, err := io.ReadFull(conn, yAs); err != nil {
				return
			}
			if _, err := io.ReadFull(conn, yAc); err != nil {
				return
			}

			serverState, yBs, err := serverKey.NewPAK(rand.Reader, false)
			if err != nil {
				s.t.Errorf("server NewPAK: %v", err)
				return
			}
			if err := serverKey.FinishPAK(&serverState, yAs); err != nil {
				s.t.Errorf("server FinishPAK: %v", err)
				return
			}

			clientState, yBc, err := clientKey.NewPAK(rand.Reader, false)
			if err != nil {
				s.t.Errorf("client NewPAK: %v", err)
				return
			}
			if err := clientKey.FinishPAK(&clientState, yAc); err != nil {
				s.t.Errorf("client FinishPAK: %v", err)
				return
			}

			reply := append([]byte{dp9ik.AuthOK}, yBs...)
			reply = append(reply, yBc...)
			if _, err := conn.Write(reply); err != nil {
				return
			}

		case dp9ik.AuthTreq:
			var ticket dp9ik.Ticket
			ticket.Num = dp9ik.AuthTc
			ticket.Form = 1
			copy(ticket.Chal[:], tr.Chal[:])
			ticket.CUID = tr.HostID
			ticket.SUID = tr.UID
			if _, err := rand.Read(ticket.Key[:]); err != nil {
				s.t.Errorf("ticket key: %v", err)
				return
			}
			clientWire, err := ticket.MarshalBinary(&clientKey)
			if err != nil {
				s.t.Errorf("client ticket: %v", err)
				return
			}
			ticket.Num = dp9ik.AuthTs
			serverWire, err := ticket.MarshalBinary(&serverKey)
			if err != nil {
				s.t.Errorf("server ticket: %v", err)
				return
			}
			if _, err := conn.Write(append([]byte{dp9ik.AuthOK}, append(clientWire, serverWire...)...)); err != nil {
				return
			}
		default:
			s.t.Errorf("unexpected auth request type %d", tr.Type)
			return
		}
	}
}

func (s *fakeService) handleCPU(raw net.Conn) {
	defer raw.Close()
	br := bufio.NewReader(raw)
	if _, err := raw.Write([]byte("v.2 dp9ik@" + s.domain + "\x00")); err != nil {
		return
	}
	choice, err := readCString(br)
	if err != nil {
		return
	}
	if choice != "dp9ik "+s.domain {
		s.t.Errorf("unexpected choice %q", choice)
		return
	}
	if _, err := raw.Write([]byte("OK\x00")); err != nil {
		return
	}

	var clientChal [dp9ik.ChalLen]byte
	if _, err := io.ReadFull(br, clientChal[:]); err != nil {
		return
	}
	serverKey := dp9ik.NewAuthKey(s.secret, s.user)
	serverState, yAs, err := serverKey.NewPAK(rand.Reader, true)
	if err != nil {
		s.t.Errorf("cpu NewPAK: %v", err)
		return
	}

	var serverChal [dp9ik.ChalLen]byte
	if _, err := rand.Read(serverChal[:]); err != nil {
		return
	}
	tr := dp9ik.TicketReq{
		Type:    dp9ik.AuthPAK,
		AuthID:  s.user,
		AuthDom: s.domain,
		HostID:  "",
		UID:     "",
	}
	copy(tr.Chal[:], serverChal[:])
	trWire, _ := tr.MarshalBinary()
	if _, err := raw.Write(append(trWire, yAs...)); err != nil {
		return
	}

	yBs := make([]byte, dp9ik.PAKYLen)
	if _, err := io.ReadFull(br, yBs); err != nil {
		return
	}
	if err := serverKey.FinishPAK(&serverState, yBs); err != nil {
		s.t.Errorf("cpu FinishPAK: %v", err)
		return
	}

	ticketWire := make([]byte, dp9ik.TicketLen)
	authWire := make([]byte, dp9ik.AuthLen)
	if _, err := io.ReadFull(br, ticketWire); err != nil {
		return
	}
	if _, err := io.ReadFull(br, authWire); err != nil {
		return
	}

	var ticket dp9ik.Ticket
	if err := ticket.UnmarshalBinary(ticketWire, &serverKey); err != nil {
		s.t.Errorf("ticket.UnmarshalBinary: %v", err)
		return
	}
	var clientAuth dp9ik.Authenticator
	if err := clientAuth.UnmarshalBinary(authWire, &ticket); err != nil {
		s.t.Errorf("auth.UnmarshalBinary: %v", err)
		return
	}
	if clientAuth.Num != dp9ik.AuthAc || clientAuth.Chal != serverChal {
		s.t.Errorf("unexpected client auth %#v", clientAuth)
		return
	}

	var serverRand [dp9ik.NonceLen]byte
	if _, err := rand.Read(serverRand[:]); err != nil {
		return
	}
	serverAuth := dp9ik.Authenticator{Num: dp9ik.AuthAs, Rand: serverRand}
	copy(serverAuth.Chal[:], clientChal[:])
	serverAuthWire, err := serverAuth.MarshalBinary(&ticket)
	if err != nil {
		s.t.Errorf("server auth marshal: %v", err)
		return
	}
	if _, err := raw.Write(serverAuthWire); err != nil {
		return
	}

	sessionSecret := mustSessionSecret(ticket.Key[:], clientAuth.Rand[:], serverRand[:])
	tlsConn := tlspsk.Server(raw, pskConfig(sessionSecret))
	if err := tlsConn.HandshakeContext(context.Background()); err != nil {
		s.t.Errorf("tls handshake: %v", err)
		return
	}

	if s.execHandler == nil && s.exportHandler == nil {
		_ = tlsConn.Close()
		return
	}
	script, err := readRemoteScript(tlsConn)
	if err != nil {
		s.t.Errorf("readRemoteScript: %v", err)
		return
	}
	switch {
	case strings.Contains(script, "exportfs -r "):
		if s.exportHandler != nil {
			s.exportHandler(script, tlsConn)
		}
	default:
		if s.execHandler != nil {
			s.execHandler(script, tlsConn)
		}
	}
}

func pskConfig(secret []byte) *tlspsk.Config {
	return &tlspsk.Config{
		InsecureSkipVerify: true,
		MinVersion:         tlspsk.VersionTLS12,
		MaxVersion:         tlspsk.VersionTLS12,
		CurvePreferences:   []tlspsk.CurveID{tlspsk.CurveP256},
		GetCertificate: func(*tlspsk.ClientHelloInfo) (*tlspsk.Certificate, error) {
			return &tlspsk.Certificate{}, nil
		},
		CipherSuites: []uint16{
			tlspsk.TLS_ECDHE_PSK_WITH_CHACHA20_POLY1305_SHA256,
			tlspsk.TLS_ECDHE_PSK_WITH_AES_128_CBC_SHA256,
			tlspsk.TLS_ECDHE_PSK_WITH_AES_256_CBC_SHA384,
			tlspsk.TLS_ECDHE_PSK_WITH_AES_128_CBC_SHA,
			tlspsk.TLS_ECDHE_PSK_WITH_AES_256_CBC_SHA,
		},
		Extra: tlspsk.PSKConfig{
			GetIdentity: func() string { return "p9secret" },
			GetKey: func(identity string) ([]byte, error) {
				if identity != "" && identity != "p9secret" {
					return nil, fmt.Errorf("unexpected identity %q", identity)
				}
				return append([]byte(nil), secret...), nil
			},
		},
	}
}

func mustSessionSecret(ticketKey, clientRand, serverRand []byte) []byte {
	reader := hkdf.New(sha256.New, ticketKey, append(append([]byte(nil), clientRand...), serverRand...), []byte("Plan 9 session secret"))
	out := make([]byte, 256)
	if _, err := io.ReadFull(reader, out); err != nil {
		panic(err)
	}
	return out
}

func readCString(r *bufio.Reader) (string, error) {
	s, err := r.ReadString(0)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(s, "\x00"), nil
}

func readRemoteScript(r io.Reader) (string, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(r, header); err != nil {
		return "", err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(header[:7])))
	if err != nil {
		return "", err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func serveNineP(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()
	paths := map[uint32]string{1: "/"}
	for {
		req, err := readFcall(conn)
		if err != nil {
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "closed") {
				return
			}
			t.Errorf("readFcall: %v", err)
			return
		}
		var resp ninep.Fcall
		switch req.Type {
		case ninep.TVERSION:
			resp = ninep.Fcall{Type: ninep.RVERSION, Tag: req.Tag, Msize: req.Msize, Version: req.Version}
		case ninep.TATTACH:
			paths[req.FID] = "/"
			resp = ninep.Fcall{Type: ninep.RATTACH, Tag: req.Tag, QID: qidForPath("/")}
		case ninep.TWALK:
			base := paths[req.FID]
			next := base
			qids := make([]ninep.QID, 0, len(req.WNames))
			for _, name := range req.WNames {
				next = path.Clean(path.Join(next, name))
				if next != "/" && next != "/hello.txt" {
					writeFcall(t, conn, ninep.Fcall{Type: ninep.RERROR, Tag: req.Tag, Ename: "not found"})
					goto nextReq
				}
				qids = append(qids, qidForPath(next))
			}
			paths[req.NewFID] = next
			resp = ninep.Fcall{Type: ninep.RWALK, Tag: req.Tag, WQIDs: qids}
		case ninep.TSTAT:
			resp = ninep.Fcall{Type: ninep.RSTAT, Tag: req.Tag, Dir: dirForPath(paths[req.FID])}
		case ninep.TCLUNK:
			delete(paths, req.FID)
			resp = ninep.Fcall{Type: ninep.RCLUNK, Tag: req.Tag}
		default:
			resp = ninep.Fcall{Type: ninep.RERROR, Tag: req.Tag, Ename: fmt.Sprintf("unsupported %d", req.Type)}
		}
		writeFcall(t, conn, resp)
	nextReq:
	}
}

func readFcall(r io.Reader) (ninep.Fcall, error) {
	sizeBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, sizeBuf); err != nil {
		return ninep.Fcall{}, err
	}
	size := int(uint32(sizeBuf[0]) | uint32(sizeBuf[1])<<8 | uint32(sizeBuf[2])<<16 | uint32(sizeBuf[3])<<24)
	body := make([]byte, size-4)
	if _, err := io.ReadFull(r, body); err != nil {
		return ninep.Fcall{}, err
	}
	return ninep.Unmarshal(append(sizeBuf, body...))
}

func writeFcall(t *testing.T, w io.Writer, f ninep.Fcall) {
	t.Helper()
	data, err := ninep.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func qidForPath(p string) ninep.QID {
	if p == "/" {
		return ninep.QID{Type: 0x80, Path: 1}
	}
	return ninep.QID{Path: 2}
}

func dirForPath(p string) *ninep.Dir {
	switch p {
	case "/":
		return &ninep.Dir{
			QID:   qidForPath("/"),
			Mode:  0x80000000 | 0o755,
			Name:  "/",
			UID:   "glenda",
			GID:   "glenda",
			MUID:  "glenda",
			Mtime: uint32(time.Now().Unix()),
		}
	case "/hello.txt":
		return &ninep.Dir{
			QID:    qidForPath("/hello.txt"),
			Mode:   0o644,
			Name:   "hello.txt",
			UID:    "glenda",
			GID:    "glenda",
			MUID:   "glenda",
			Length: 5,
			Mtime:  uint32(time.Now().Unix()),
		}
	default:
		return &ninep.Dir{Name: path.Base(p)}
	}
}

func bytesJoin(chunks [][]byte) []byte {
	n := 0
	for _, chunk := range chunks {
		n += len(chunk)
	}
	out := make([]byte, 0, n)
	for _, chunk := range chunks {
		out = append(out, chunk...)
	}
	return out
}
