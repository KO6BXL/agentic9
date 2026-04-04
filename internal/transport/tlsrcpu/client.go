package tlsrcpu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"agentic9/internal/auth/dp9ik"
	"agentic9/internal/config"

	tlspsk "github.com/jc-lab/go-tls-psk"
)

const rcpuPort = "17019"

type Client struct {
	profile config.Profile
	secret  config.Secret
}

func NewClient(profile config.Profile, secret config.Secret) *Client {
	return &Client{profile: profile, secret: secret}
}

func (c *Client) Verify(ctx context.Context) error {
	if err := c.validateSecret(); err != nil {
		return err
	}
	conn, err := c.connectTLS(ctx)
	if err != nil {
		return err
	}
	return conn.Close()
}

func (c *Client) Exec(ctx context.Context, script string, output func([]byte) error) error {
	if err := c.validateSecret(); err != nil {
		return err
	}
	conn, err := c.connectTLS(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := sendRemoteScript(conn, script); err != nil {
		return err
	}
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	return streamUntilEOF(ctx, conn, output)
}

func (c *Client) EnsureRemoteDir(ctx context.Context, remoteRoot string) error {
	return c.Exec(ctx, fmt.Sprintf("mkdir -p %s\n", rcQuote(remoteRoot)), nil)
}

func (c *Client) RemoveRemoteTree(ctx context.Context, remoteRoot string) error {
	return c.Exec(ctx, fmt.Sprintf("rm -rf %s\n", rcQuote(remoteRoot)), nil)
}

func (c *Client) OpenExportFS(ctx context.Context, remoteRoot string) (io.ReadWriteCloser, error) {
	if err := c.validateSecret(); err != nil {
		return nil, err
	}
	conn, err := c.connectTLS(ctx)
	if err != nil {
		return nil, err
	}
	if err := sendRemoteScript(conn, fmt.Sprintf("exec exportfs -r %s\n", rcQuote(remoteRoot))); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (c *Client) validateSecret() error {
	if strings.TrimSpace(c.secret.Value) == "" {
		return fmt.Errorf("secret from %s is empty", c.secret.Source)
	}
	return nil
}

func (c *Client) connectTLS(ctx context.Context) (net.Conn, error) {
	dialer := &net.Dialer{}
	rawConn, err := dialer.DialContext(ctx, "tcp", serviceAddress(c.profile.CPUHost, rcpuPort))
	if err != nil {
		return nil, err
	}
	session, err := dp9ik.Authenticate(ctx, rawConn, c.profile, c.secret.Value)
	if err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	cfg := &tlspsk.Config{
		InsecureSkipVerify: true,
		MinVersion:         tlspsk.VersionTLS12,
		MaxVersion:         tlspsk.VersionTLS12,
		CurvePreferences:   []tlspsk.CurveID{tlspsk.CurveP256},
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
					return nil, fmt.Errorf("unexpected psk identity %q", identity)
				}
				return append([]byte(nil), session.SessionSecret...), nil
			},
		},
	}
	tlsConn := tlspsk.Client(rawConn, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func sendRemoteScript(w io.Writer, script string) error {
	_, err := fmt.Fprintf(w, "%7d\n%s", len(script), script)
	return err
}

func streamUntilEOF(ctx context.Context, conn net.Conn, output func([]byte) error) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 && output != nil {
			chunk := append([]byte(nil), buf[:n]...)
			if cbErr := output(chunk); cbErr != nil {
				_ = conn.Close()
				return cbErr
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
}

func rcQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func serviceAddress(host, port string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, port)
}
