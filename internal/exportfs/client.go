package exportfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"agentic9/internal/ninep"
	"agentic9/internal/transport/rcpu"
)

type StreamOpener interface {
	rcpu.ExportFSOpener
}

type Client struct {
	opener     StreamOpener
	remoteRoot string

	mu     sync.Mutex
	client *ninep.Client
}

type Entry struct {
	Path     string
	Mode     os.FileMode
	Size     uint64
	ModTime  time.Time
	Linkname string
}

type FileHandle interface {
	io.ReaderAt
	io.WriterAt
	Close() error
	Flush() error
}

func New(opener StreamOpener, remoteRoot string) *Client {
	return &Client{opener: opener, remoteRoot: remoteRoot}
}

func (c *Client) Stat(ctx context.Context, p string) (Entry, error) {
	cli, fid, dir, err := c.walkTo(ctx, p)
	if err != nil {
		return Entry{}, err
	}
	defer cli.Clunk(context.Background(), fid)
	return entryFromDir(p, dir), nil
}

func (c *Client) List(ctx context.Context, p string) ([]Entry, error) {
	return nil, errors.New("remote directory listing is not implemented yet")
}

func (c *Client) Open(ctx context.Context, p string, flags uint32) (FileHandle, Entry, error) {
	cli, fid, dir, err := c.walkTo(ctx, p)
	if err != nil {
		return nil, Entry{}, err
	}
	mode := uint8(0)
	if flags&uint32(os.O_WRONLY) != 0 {
		mode = 1
	}
	if flags&uint32(os.O_RDWR) != 0 {
		mode = 2
	}
	if _, _, err := cli.Open(ctx, fid, mode); err != nil {
		_ = cli.Clunk(context.Background(), fid)
		return nil, Entry{}, err
	}
	return &remoteFile{client: cli, fid: fid}, entryFromDir(p, dir), nil
}

func (c *Client) Create(ctx context.Context, p string, perm os.FileMode, flags uint32) (FileHandle, Entry, error) {
	dirName, base := path.Split(cleanPath(p))
	cli, dirFID, _, err := c.walkTo(ctx, dirName)
	if err != nil {
		return nil, Entry{}, err
	}
	mode := uint8(0)
	if flags&uint32(os.O_RDWR) != 0 {
		mode = 2
	}
	qid, _, err := cli.Create(ctx, dirFID, base, uint32(perm.Perm()), mode)
	if err != nil {
		_ = cli.Clunk(context.Background(), dirFID)
		return nil, Entry{}, err
	}
	entry := Entry{Path: p, Mode: perm, ModTime: time.Now(), Size: 0}
	return &remoteFile{client: cli, fid: dirFID}, withQID(entry, qid), nil
}

func (c *Client) Mkdir(ctx context.Context, p string, perm os.FileMode) error {
	_, _, err := c.Create(ctx, p, perm|os.ModeDir, 0)
	return err
}

func (c *Client) Remove(ctx context.Context, p string) error {
	cli, fid, _, err := c.walkTo(ctx, p)
	if err != nil {
		return err
	}
	return cli.Remove(ctx, fid)
}

func (c *Client) Rename(ctx context.Context, oldPath, newPath string) error {
	cli, fid, dir, err := c.walkTo(ctx, oldPath)
	if err != nil {
		return err
	}
	defer cli.Clunk(context.Background(), fid)
	copyDir := *dir
	copyDir.Name = path.Base(newPath)
	return cli.Wstat(ctx, fid, &copyDir)
}

func (c *Client) Chmod(ctx context.Context, p string, mode os.FileMode) error {
	cli, fid, dir, err := c.walkTo(ctx, p)
	if err != nil {
		return err
	}
	defer cli.Clunk(context.Background(), fid)
	copyDir := *dir
	copyDir.Mode = uint32(mode.Perm())
	return cli.Wstat(ctx, fid, &copyDir)
}

func (c *Client) Truncate(ctx context.Context, p string, size uint64) error {
	cli, fid, dir, err := c.walkTo(ctx, p)
	if err != nil {
		return err
	}
	defer cli.Clunk(context.Background(), fid)
	copyDir := *dir
	copyDir.Length = size
	return cli.Wstat(ctx, fid, &copyDir)
}

func (c *Client) Symlink(ctx context.Context, target, newPath string) error {
	_ = ctx
	_ = target
	_ = newPath
	return errors.New("remote symlink creation is not implemented yet")
}

func (c *Client) Readlink(ctx context.Context, p string) (string, error) {
	_ = ctx
	_ = p
	return "", errors.New("remote symlink reads are not implemented yet")
}

func (c *Client) connect(ctx context.Context) (*ninep.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil {
		return c.client, nil
	}
	stream, err := c.opener.OpenExportFS(ctx, c.remoteRoot)
	if err != nil {
		return nil, err
	}
	cli := ninep.NewClient(stream)
	if err := cli.Version(ctx, "9P2000"); err != nil {
		return nil, err
	}
	if err := cli.Attach(ctx, 1, "", ""); err != nil {
		return nil, err
	}
	c.client = cli
	return c.client, nil
}

func (c *Client) walkTo(ctx context.Context, p string) (*ninep.Client, uint32, *ninep.Dir, error) {
	cli, err := c.connect(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	clean := cleanPath(p)
	fid := uint32(time.Now().UnixNano())
	if clean == "/" {
		if _, err := cli.Walk(ctx, 1, fid, nil); err != nil {
			return nil, 0, nil, err
		}
	} else {
		names := strings.Split(strings.TrimPrefix(clean, "/"), "/")
		if _, err := cli.Walk(ctx, 1, fid, names); err != nil {
			return nil, 0, nil, err
		}
	}
	dir, err := cli.Stat(ctx, fid)
	if err != nil {
		_ = cli.Clunk(context.Background(), fid)
		return nil, 0, nil, err
	}
	return cli, fid, dir, nil
}

func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	return path.Clean("/" + p)
}

func entryFromDir(p string, dir *ninep.Dir) Entry {
	mode := os.FileMode(dir.Mode & 0o777)
	if dir.Mode&0x80000000 != 0 {
		mode |= os.ModeDir
	}
	return Entry{
		Path:    p,
		Mode:    mode,
		Size:    dir.Length,
		ModTime: dir.ModTime(),
	}
}

func withQID(entry Entry, _ ninep.QID) Entry { return entry }

type remoteFile struct {
	client *ninep.Client
	fid    uint32
}

func (f *remoteFile) ReadAt(p []byte, off int64) (int, error) {
	data, err := f.client.Read(context.Background(), f.fid, uint64(off), uint32(len(p)))
	if err != nil {
		return 0, err
	}
	n := copy(p, data)
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (f *remoteFile) WriteAt(p []byte, off int64) (int, error) {
	n, err := f.client.Write(context.Background(), f.fid, uint64(off), p)
	return int(n), err
}

func (f *remoteFile) Close() error {
	return f.client.Clunk(context.Background(), f.fid)
}

func (f *remoteFile) Flush() error { return nil }

func (c *Client) String() string {
	return fmt.Sprintf("exportfs(%s)", c.remoteRoot)
}
