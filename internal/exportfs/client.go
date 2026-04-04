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
	"syscall"
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

	mu        sync.Mutex
	client    *ninep.Client
	unhealthy error
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

var (
	ErrNotSupported = errors.New("exportfs operation is not supported by the remote backend")
	ErrDisconnected = errors.New("exportfs connection is no longer healthy")
)

func New(opener StreamOpener, remoteRoot string) *Client {
	return &Client{opener: opener, remoteRoot: remoteRoot}
}

func (c *Client) Stat(ctx context.Context, p string) (Entry, error) {
	cli, fid, dir, err := c.walkTo(ctx, p)
	if err != nil {
		return Entry{}, c.recordError(err)
	}
	defer cli.Clunk(context.Background(), fid)
	return entryFromDir(p, dir), nil
}

func (c *Client) List(ctx context.Context, p string) ([]Entry, error) {
	cli, fid, _, err := c.walkTo(ctx, p)
	if err != nil {
		return nil, c.recordError(err)
	}
	defer cli.Clunk(context.Background(), fid)
	if _, _, err := cli.Open(ctx, fid, 0); err != nil {
		return nil, c.recordError(err)
	}
	var (
		entries []Entry
		offset  uint64
	)
	for {
		data, err := cli.Read(ctx, fid, offset, 8192)
		if err != nil {
			return nil, c.recordError(err)
		}
		if len(data) == 0 {
			return entries, nil
		}
		dirs, err := ninep.ParseDirEntries(data)
		if err != nil {
			return nil, c.recordError(err)
		}
		for i := range dirs {
			entries = append(entries, entryFromDir(path.Join(cleanPath(p), dirs[i].Name), &dirs[i]))
		}
		offset += uint64(len(data))
	}
}

func (c *Client) Open(ctx context.Context, p string, flags uint32) (FileHandle, Entry, error) {
	cli, fid, dir, err := c.walkTo(ctx, p)
	if err != nil {
		return nil, Entry{}, c.recordError(err)
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
		return nil, Entry{}, c.recordError(err)
	}
	return &remoteFile{owner: c, client: cli, fid: fid}, entryFromDir(p, dir), nil
}

func (c *Client) Create(ctx context.Context, p string, perm os.FileMode, flags uint32) (FileHandle, Entry, error) {
	dirName, base := path.Split(cleanPath(p))
	cli, dirFID, _, err := c.walkTo(ctx, dirName)
	if err != nil {
		return nil, Entry{}, c.recordError(err)
	}
	mode := uint8(0)
	if flags&uint32(os.O_WRONLY) != 0 {
		mode = 1
	}
	if flags&uint32(os.O_RDWR) != 0 {
		mode = 2
	}
	qid, _, err := cli.Create(ctx, dirFID, base, permBits(perm), mode)
	if err != nil {
		_ = cli.Clunk(context.Background(), dirFID)
		return nil, Entry{}, c.recordError(err)
	}
	entry := Entry{Path: p, Mode: perm, ModTime: time.Now(), Size: 0}
	return &remoteFile{owner: c, client: cli, fid: dirFID}, withQID(entry, qid), nil
}

func (c *Client) Mkdir(ctx context.Context, p string, perm os.FileMode) error {
	_, _, err := c.Create(ctx, p, perm|os.ModeDir, 0)
	return c.recordError(err)
}

func (c *Client) Remove(ctx context.Context, p string) error {
	cli, fid, _, err := c.walkTo(ctx, p)
	if err != nil {
		return c.recordError(err)
	}
	return c.recordError(cli.Remove(ctx, fid))
}

func (c *Client) Rename(ctx context.Context, oldPath, newPath string) error {
	cli, fid, dir, err := c.walkTo(ctx, oldPath)
	if err != nil {
		return c.recordError(err)
	}
	defer cli.Clunk(context.Background(), fid)
	copyDir := *dir
	copyDir.Name = path.Base(newPath)
	return c.recordError(cli.Wstat(ctx, fid, &copyDir))
}

func (c *Client) Chmod(ctx context.Context, p string, mode os.FileMode) error {
	cli, fid, dir, err := c.walkTo(ctx, p)
	if err != nil {
		return c.recordError(err)
	}
	defer cli.Clunk(context.Background(), fid)
	copyDir := *dir
	copyDir.Mode = uint32(mode.Perm())
	return c.recordError(cli.Wstat(ctx, fid, &copyDir))
}

func (c *Client) Truncate(ctx context.Context, p string, size uint64) error {
	cli, fid, dir, err := c.walkTo(ctx, p)
	if err != nil {
		return c.recordError(err)
	}
	defer cli.Clunk(context.Background(), fid)
	copyDir := *dir
	copyDir.Length = size
	return c.recordError(cli.Wstat(ctx, fid, &copyDir))
}

func (c *Client) Symlink(ctx context.Context, target, newPath string) error {
	_ = ctx
	_ = target
	_ = newPath
	return ErrNotSupported
}

func (c *Client) Readlink(ctx context.Context, p string) (string, error) {
	_ = ctx
	_ = p
	return "", ErrNotSupported
}

func (c *Client) connect(ctx context.Context) (*ninep.Client, error) {
	c.mu.Lock()
	if c.unhealthy != nil {
		err := c.unhealthy
		c.mu.Unlock()
		return nil, err
	}
	if c.client != nil {
		cli := c.client
		c.mu.Unlock()
		return cli, nil
	}
	c.mu.Unlock()

	stream, err := c.opener.OpenExportFS(ctx, c.remoteRoot)
	if err != nil {
		return nil, c.recordError(err)
	}
	cli := ninep.NewClient(stream)
	if err := cli.Version(ctx, "9P2000"); err != nil {
		_ = cli.Close()
		return nil, c.recordError(err)
	}
	if err := cli.Attach(ctx, 1, "", ""); err != nil {
		_ = cli.Close()
		return nil, c.recordError(err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unhealthy != nil {
		_ = cli.Close()
		return nil, c.unhealthy
	}
	if c.client != nil {
		_ = cli.Close()
		return c.client, nil
	}
	c.client = cli
	return cli, nil
}

func (c *Client) walkTo(ctx context.Context, p string) (*ninep.Client, uint32, *ninep.Dir, error) {
	cli, err := c.connect(ctx)
	if err != nil {
		return nil, 0, nil, c.recordError(err)
	}
	clean := cleanPath(p)
	fid := cli.AllocFID()
	if clean == "/" {
		if _, err := cli.Walk(ctx, 1, fid, nil); err != nil {
			return nil, 0, nil, c.recordError(err)
		}
	} else {
		names := strings.Split(strings.TrimPrefix(clean, "/"), "/")
		if _, err := cli.Walk(ctx, 1, fid, names); err != nil {
			return nil, 0, nil, c.recordError(err)
		}
	}
	dir, err := cli.Stat(ctx, fid)
	if err != nil {
		_ = cli.Clunk(context.Background(), fid)
		return nil, 0, nil, c.recordError(err)
	}
	return cli, fid, dir, nil
}

func (c *Client) recordError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotSupported) || errors.Is(err, ErrDisconnected) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if normalized := normalizeRemoteError(err); normalized != nil {
		return normalized
	}
	if !isDisconnectError(err) {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.unhealthy = ErrDisconnected
	if c.client != nil {
		_ = c.client.Close()
		c.client = nil
	}
	return ErrDisconnected
}

func normalizeRemoteError(err error) error {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "directory entry not found"),
		strings.Contains(msg, "does not exist"),
		strings.Contains(msg, "not found"):
		return fmt.Errorf("%w: %v", os.ErrNotExist, err)
	case strings.Contains(msg, "permission denied"):
		return fmt.Errorf("%w: %v", os.ErrPermission, err)
	case strings.Contains(msg, "file exists"),
		strings.Contains(msg, "already exists"):
		return fmt.Errorf("%w: %v", os.ErrExist, err)
	default:
		return nil
	}
}

func isDisconnectError(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, os.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET)
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

func permBits(mode os.FileMode) uint32 {
	perm := uint32(mode.Perm())
	if mode&os.ModeDir != 0 {
		perm |= 0x80000000
	}
	return perm
}

func withQID(entry Entry, _ ninep.QID) Entry { return entry }

type remoteFile struct {
	owner  *Client
	client *ninep.Client
	fid    uint32
}

func (f *remoteFile) ReadAt(p []byte, off int64) (int, error) {
	data, err := f.client.Read(context.Background(), f.fid, uint64(off), uint32(len(p)))
	if err != nil {
		return 0, f.owner.recordError(err)
	}
	n := copy(p, data)
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (f *remoteFile) WriteAt(p []byte, off int64) (int, error) {
	n, err := f.client.Write(context.Background(), f.fid, uint64(off), p)
	return int(n), f.owner.recordError(err)
}

func (f *remoteFile) Close() error {
	return f.owner.recordError(f.client.Clunk(context.Background(), f.fid))
}

func (f *remoteFile) Flush() error { return nil }

func (c *Client) String() string {
	return fmt.Sprintf("exportfs(%s)", c.remoteRoot)
}
