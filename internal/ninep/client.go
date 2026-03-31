package ninep

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
)

type Client struct {
	rw    io.ReadWriteCloser
	br    *bufio.Reader
	mu    sync.Mutex
	msize uint32
	tag   uint16
	fid   uint32
}

func NewClient(rw io.ReadWriteCloser) *Client {
	return &Client{rw: rw, br: bufio.NewReader(rw), msize: 8192, tag: 1, fid: 2}
}

func (c *Client) Close() error { return c.rw.Close() }

func (c *Client) AllocFID() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	fid := c.fid
	c.fid++
	if c.fid == NOFID {
		c.fid = 2
	}
	return fid
}

func (c *Client) Version(ctx context.Context, version string) error {
	_, err := c.rpc(ctx, Fcall{Type: TVERSION, Tag: NOTAG, Msize: c.msize, Version: version})
	return err
}

func (c *Client) Attach(ctx context.Context, fid uint32, user, aname string) error {
	_, err := c.rpc(ctx, Fcall{Type: TATTACH, Tag: c.nextTag(), FID: fid, AFID: NOFID, UNAME: user, ANAME: aname})
	return err
}

func (c *Client) Walk(ctx context.Context, fid, newfid uint32, names []string) ([]QID, error) {
	resp, err := c.rpc(ctx, Fcall{Type: TWALK, Tag: c.nextTag(), FID: fid, NewFID: newfid, WNames: names})
	if err != nil {
		return nil, err
	}
	return resp.WQIDs, nil
}

func (c *Client) Open(ctx context.Context, fid uint32, mode uint8) (QID, uint32, error) {
	resp, err := c.rpc(ctx, Fcall{Type: TOPEN, Tag: c.nextTag(), FID: fid, Mode: mode})
	if err != nil {
		return QID{}, 0, err
	}
	return resp.QID, resp.Iounit, nil
}

func (c *Client) Create(ctx context.Context, fid uint32, name string, perm uint32, mode uint8) (QID, uint32, error) {
	resp, err := c.rpc(ctx, Fcall{Type: TCREATE, Tag: c.nextTag(), FID: fid, Name: name, Perm: perm, Mode: mode})
	if err != nil {
		return QID{}, 0, err
	}
	return resp.QID, resp.Iounit, nil
}

func (c *Client) Read(ctx context.Context, fid uint32, offset uint64, count uint32) ([]byte, error) {
	resp, err := c.rpc(ctx, Fcall{Type: TREAD, Tag: c.nextTag(), FID: fid, Offset: offset, Count: count})
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *Client) Write(ctx context.Context, fid uint32, offset uint64, data []byte) (uint32, error) {
	resp, err := c.rpc(ctx, Fcall{Type: TWRITE, Tag: c.nextTag(), FID: fid, Offset: offset, Data: data})
	if err != nil {
		return 0, err
	}
	return resp.Count, nil
}

func (c *Client) Clunk(ctx context.Context, fid uint32) error {
	_, err := c.rpc(ctx, Fcall{Type: TCLUNK, Tag: c.nextTag(), FID: fid})
	return err
}

func (c *Client) Remove(ctx context.Context, fid uint32) error {
	_, err := c.rpc(ctx, Fcall{Type: TREMOVE, Tag: c.nextTag(), FID: fid})
	return err
}

func (c *Client) Stat(ctx context.Context, fid uint32) (*Dir, error) {
	resp, err := c.rpc(ctx, Fcall{Type: TSTAT, Tag: c.nextTag(), FID: fid})
	if err != nil {
		return nil, err
	}
	return resp.Dir, nil
}

func (c *Client) Wstat(ctx context.Context, fid uint32, dir *Dir) error {
	_, err := c.rpc(ctx, Fcall{Type: TWSTAT, Tag: c.nextTag(), FID: fid, Dir: dir})
	return err
}

func (c *Client) rpc(ctx context.Context, req Fcall) (Fcall, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Fcall{}, err
	}
	data, err := Marshal(req)
	if err != nil {
		return Fcall{}, err
	}
	if _, err := c.rw.Write(data); err != nil {
		return Fcall{}, err
	}
	sizeBuf := make([]byte, 4)
	if _, err := io.ReadFull(c.br, sizeBuf); err != nil {
		return Fcall{}, err
	}
	size := int(uint32(sizeBuf[0]) | uint32(sizeBuf[1])<<8 | uint32(sizeBuf[2])<<16 | uint32(sizeBuf[3])<<24)
	body := make([]byte, size-4)
	if _, err := io.ReadFull(c.br, body); err != nil {
		return Fcall{}, err
	}
	resp, err := Unmarshal(append(sizeBuf, body...))
	if err != nil {
		return Fcall{}, err
	}
	if resp.Type == RERROR {
		return Fcall{}, fmt.Errorf("9p error: %s", resp.Ename)
	}
	return resp, nil
}

func (c *Client) nextTag() uint16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	tag := c.tag
	c.tag++
	if c.tag == NOTAG {
		c.tag = 1
	}
	return tag
}
