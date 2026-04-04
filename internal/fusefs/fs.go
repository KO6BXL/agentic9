package fusefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"agentic9/internal/exportfs"

	gofuse "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type MountManager struct {
	root string

	mu     sync.Mutex
	mounts map[string]*Handle
}

type Handle struct {
	server     *fuse.Server
	mountpoint string
}

func NewMountManager(root string) *MountManager {
	return &MountManager{root: root, mounts: map[string]*Handle{}}
}

func (m *MountManager) Mount(ctx context.Context, profile, agentID, mountpoint string, backend *exportfs.Client) (*Handle, error) {
	_ = ctx
	_ = profile
	_ = agentID
	if err := os.MkdirAll(filepath.Dir(mountpoint), 0o755); err != nil {
		return nil, err
	}
	timeout := 1 * time.Second
	root := &node{backend: backend, path: "/"}
	debugEnabled := os.Getenv("AGENTIC9_FUSE_DEBUG") != ""
	server, err := gofuse.Mount(mountpoint, root, &gofuse.Options{
		MountOptions: fuse.MountOptions{
			AllowOther:     false,
			Name:           "agentic9",
			FsName:         "agentic9",
			DisableXAttrs:  true,
			MaxWrite:       128 * 1024,
			Debug:          debugEnabled,
			RememberInodes: true,
		},
		EntryTimeout:    &timeout,
		AttrTimeout:     &timeout,
		NullPermissions: true,
	})
	if err != nil {
		return nil, err
	}
	handle := &Handle{server: server, mountpoint: mountpoint}
	m.mu.Lock()
	m.mounts[mountpoint] = handle
	m.mu.Unlock()
	go server.Wait()
	return handle, nil
}

func (m *MountManager) Unmount(mountpoint string) error {
	m.mu.Lock()
	handle, ok := m.mounts[mountpoint]
	if ok {
		delete(m.mounts, mountpoint)
	}
	m.mu.Unlock()
	if !ok {
		return unmountPath(mountpoint)
	}
	return handle.Close()
}

func (h *Handle) Close() error {
	if h.server == nil {
		return nil
	}
	return h.server.Unmount()
}

func (h *Handle) Wait() error {
	if h.server == nil {
		return nil
	}
	h.server.Wait()
	return nil
}

func (h *Handle) PID() int { return os.Getpid() }

type node struct {
	gofuse.Inode
	backend *exportfs.Client
	path    string
}

var (
	_ gofuse.NodeGetattrer  = (*node)(nil)
	_ gofuse.NodeLookuper   = (*node)(nil)
	_ gofuse.NodeReaddirer  = (*node)(nil)
	_ gofuse.NodeOpener     = (*node)(nil)
	_ gofuse.NodeCreater    = (*node)(nil)
	_ gofuse.NodeMknoder    = (*node)(nil)
	_ gofuse.NodeMkdirer    = (*node)(nil)
	_ gofuse.NodeUnlinker   = (*node)(nil)
	_ gofuse.NodeRmdirer    = (*node)(nil)
	_ gofuse.NodeRenamer    = (*node)(nil)
	_ gofuse.NodeSetattrer  = (*node)(nil)
	_ gofuse.NodeReadlinker = (*node)(nil)
	_ gofuse.NodeSymlinker  = (*node)(nil)
)

func (n *node) Getattr(ctx context.Context, _ gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	entry, err := n.backend.Stat(ctx, n.path)
	if err != nil {
		return errno(err)
	}
	fillAttr(out, entry)
	return 0
}

func (n *node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*gofuse.Inode, syscall.Errno) {
	childPath := JoinPath(n.path, name)
	entry, err := n.backend.Stat(ctx, childPath)
	if err != nil {
		return nil, errno(err)
	}
	stable := gofuse.StableAttr{Mode: modeBits(entry.Mode)}
	child := &node{backend: n.backend, path: childPath}
	fillEntry(out, entry)
	return n.NewInode(ctx, child, stable), 0
}

func (n *node) Readdir(ctx context.Context) (gofuse.DirStream, syscall.Errno) {
	entries, err := n.backend.List(ctx, n.path)
	if err != nil {
		return nil, errno(err)
	}
	out := make([]fuse.DirEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, fuse.DirEntry{
			Name: filepath.Base(entry.Path),
			Mode: modeBits(entry.Mode),
		})
	}
	return gofuse.NewListDirStream(out), 0
}

func (n *node) Open(ctx context.Context, flags uint32) (gofuse.FileHandle, uint32, syscall.Errno) {
	handle, _, err := n.backend.Open(ctx, n.path, flags)
	if err != nil {
		return nil, 0, errno(err)
	}
	return &fileHandle{file: handle}, fuse.FOPEN_DIRECT_IO, 0
}

func (n *node) Create(ctx context.Context, name string, flags, mode uint32, out *fuse.EntryOut) (*gofuse.Inode, gofuse.FileHandle, uint32, syscall.Errno) {
	childPath := JoinPath(n.path, name)
	debugf("Create path=%s flags=%#x mode=%#o", childPath, flags, mode)
	handle, entry, err := n.backend.Create(ctx, childPath, os.FileMode(mode), flags)
	if err != nil {
		debugf("Create error path=%s err=%v", childPath, err)
		return nil, nil, 0, errno(err)
	}
	fillEntry(out, entry)
	child := &node{backend: n.backend, path: childPath}
	return n.NewInode(ctx, child, gofuse.StableAttr{Mode: modeBits(entry.Mode)}), &fileHandle{file: handle}, fuse.FOPEN_DIRECT_IO, 0
}

func (n *node) Mknod(ctx context.Context, name string, mode, dev uint32, out *fuse.EntryOut) (*gofuse.Inode, syscall.Errno) {
	_ = dev
	childPath := JoinPath(n.path, name)
	debugf("Mknod path=%s mode=%#o", childPath, mode)
	fileType := mode & syscall.S_IFMT
	switch fileType {
	case 0, syscall.S_IFREG:
	default:
		debugf("Mknod unsupported path=%s mode=%#o", childPath, mode)
		return nil, syscall.ENOSYS
	}

	handle, entry, err := n.backend.Create(ctx, childPath, os.FileMode(mode), uint32(os.O_WRONLY))
	if err != nil {
		debugf("Mknod error path=%s err=%v", childPath, err)
		return nil, errno(err)
	}
	if cerr := handle.Close(); cerr != nil {
		debugf("Mknod close error path=%s err=%v", childPath, cerr)
		return nil, errno(cerr)
	}
	fillEntry(out, entry)
	child := &node{backend: n.backend, path: childPath}
	return n.NewInode(ctx, child, gofuse.StableAttr{Mode: modeBits(entry.Mode)}), 0
}

func (n *node) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*gofuse.Inode, syscall.Errno) {
	childPath := JoinPath(n.path, name)
	if err := n.backend.Mkdir(ctx, childPath, os.FileMode(mode)|os.ModeDir); err != nil {
		return nil, errno(err)
	}
	entry, err := n.backend.Stat(ctx, childPath)
	if err != nil {
		return nil, errno(err)
	}
	fillEntry(out, entry)
	child := &node{backend: n.backend, path: childPath}
	return n.NewInode(ctx, child, gofuse.StableAttr{Mode: fuse.S_IFDIR}), 0
}

func (n *node) Unlink(ctx context.Context, name string) syscall.Errno {
	return errno(n.backend.Remove(ctx, JoinPath(n.path, name)))
}

func (n *node) Rmdir(ctx context.Context, name string) syscall.Errno {
	return errno(n.backend.Remove(ctx, JoinPath(n.path, name)))
}

func (n *node) Rename(ctx context.Context, name string, newParent gofuse.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	_ = flags
	parent, ok := newParent.(*node)
	if !ok {
		return syscall.EIO
	}
	return errno(n.backend.Rename(ctx, JoinPath(n.path, name), JoinPath(parent.path, newName)))
}

func (n *node) Setattr(ctx context.Context, _ gofuse.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if in.Valid&fuse.FATTR_MODE != 0 {
		if err := n.backend.Chmod(ctx, n.path, os.FileMode(in.Mode)); err != nil {
			return errno(err)
		}
	}
	if in.Valid&fuse.FATTR_SIZE != 0 {
		if err := n.backend.Truncate(ctx, n.path, in.Size); err != nil {
			return errno(err)
		}
	}
	entry, err := n.backend.Stat(ctx, n.path)
	if err != nil {
		return errno(err)
	}
	fillAttr(out, entry)
	return 0
}

func (n *node) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	target, err := n.backend.Readlink(ctx, n.path)
	if err != nil {
		return nil, errno(err)
	}
	return []byte(target), 0
}

func (n *node) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*gofuse.Inode, syscall.Errno) {
	childPath := JoinPath(n.path, name)
	if err := n.backend.Symlink(ctx, target, childPath); err != nil {
		return nil, errno(err)
	}
	entry, err := n.backend.Stat(ctx, childPath)
	if err != nil {
		return nil, errno(err)
	}
	fillEntry(out, entry)
	child := &node{backend: n.backend, path: childPath}
	return n.NewInode(ctx, child, gofuse.StableAttr{Mode: modeBits(entry.Mode)}), 0
}

type fileHandle struct {
	file exportfs.FileHandle
}

var (
	_ gofuse.FileReader  = (*fileHandle)(nil)
	_ gofuse.FileWriter  = (*fileHandle)(nil)
	_ gofuse.FileFlusher = (*fileHandle)(nil)
	_ gofuse.FileFsyncer = (*fileHandle)(nil)
)

func (f *fileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	_ = ctx
	n, err := f.file.ReadAt(dest, off)
	if err != nil && !errors.Is(err, os.ErrClosed) {
		if !errors.Is(err, io.EOF) {
			return nil, errno(err)
		}
	}
	return fuse.ReadResultData(dest[:n]), 0
}

func (f *fileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	_ = ctx
	n, err := f.file.WriteAt(data, off)
	return uint32(n), errno(err)
}

func (f *fileHandle) Flush(ctx context.Context) syscall.Errno {
	_ = ctx
	return errno(f.file.Flush())
}

func (f *fileHandle) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	_ = ctx
	_ = flags
	return errno(f.file.Flush())
}

func errno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	if errors.Is(err, exportfs.ErrNotSupported) {
		return syscall.ENOSYS
	}
	if errors.Is(err, exportfs.ErrDisconnected) {
		return syscall.EIO
	}
	if errors.Is(err, os.ErrNotExist) {
		return syscall.ENOENT
	}
	if errors.Is(err, os.ErrPermission) {
		return syscall.EPERM
	}
	return syscall.EIO
}

func fillEntry(out *fuse.EntryOut, entry exportfs.Entry) {
	out.Attr.Mode = modeBits(entry.Mode)
	out.Attr.Size = entry.Size
	out.SetEntryTimeout(1 * time.Second)
	out.SetAttrTimeout(1 * time.Second)
	out.Attr.SetTimes(&entry.ModTime, &entry.ModTime, &entry.ModTime)
}

func fillAttr(out *fuse.AttrOut, entry exportfs.Entry) {
	out.Mode = modeBits(entry.Mode)
	out.Size = entry.Size
	out.SetTimes(&entry.ModTime, &entry.ModTime, &entry.ModTime)
}

func modeBits(mode os.FileMode) uint32 {
	var out uint32 = uint32(mode.Perm())
	if mode.IsDir() {
		out |= fuse.S_IFDIR
	} else if mode&os.ModeSymlink != 0 {
		out |= fuse.S_IFLNK
	} else {
		out |= fuse.S_IFREG
	}
	return out
}

func unmountPath(mountpoint string) error {
	for _, bin := range []string{"fusermount3", "fusermount"} {
		if _, err := exec.LookPath(bin); err == nil {
			cmd := exec.Command(bin, "-u", mountpoint)
			if err := cmd.Run(); err == nil {
				return nil
			}
		}
	}
	return syscall.ENOSYS
}

func debugf(format string, args ...any) {
	if os.Getenv("AGENTIC9_FUSE_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "agentic9 fuse: "+format+"\n", args...)
}
