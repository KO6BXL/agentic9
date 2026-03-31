package tlsrcpu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"agentic9/internal/auth/dp9ik"
	"agentic9/internal/config"
	"agentic9/internal/transport/rcpu"
)

var ErrUnimplemented = errors.New("9front tls rcpu client is not implemented yet")

type Client struct {
	profile config.Profile
	secret  config.Secret
}

func NewClient(profile config.Profile, secret config.Secret) *Client {
	return &Client{profile: profile, secret: secret}
}

func (c *Client) Verify(ctx context.Context) error {
	_ = ctx
	if strings.TrimSpace(c.secret.Value) == "" {
		return fmt.Errorf("secret from %s is empty", c.secret.Source)
	}
	return ErrUnimplemented
}

func (c *Client) Exec(ctx context.Context, script string, output func([]byte) error) error {
	_ = ctx
	_ = script
	_ = output
	return rcpu.ErrUnimplemented
}

func (c *Client) EnsureRemoteDir(ctx context.Context, remoteRoot string) error {
	return c.Exec(ctx, fmt.Sprintf("mkdir -p %q", remoteRoot), nil)
}

func (c *Client) RemoveRemoteTree(ctx context.Context, remoteRoot string) error {
	return c.Exec(ctx, fmt.Sprintf("rm -rf %q", remoteRoot), nil)
}

func (c *Client) OpenExportFS(ctx context.Context, remoteRoot string) (io.ReadWriteCloser, error) {
	_ = ctx
	_ = remoteRoot
	return nil, dp9ik.ErrUnimplemented
}
