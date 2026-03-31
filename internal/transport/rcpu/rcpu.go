package rcpu

import (
	"context"
	"errors"
	"io"
)

var ErrUnimplemented = errors.New("rcpu transport is not implemented yet")

type Executor interface {
	Exec(ctx context.Context, script string, output func([]byte) error) error
}

type ExportFSOpener interface {
	OpenExportFS(ctx context.Context, remoteRoot string) (io.ReadWriteCloser, error)
}
