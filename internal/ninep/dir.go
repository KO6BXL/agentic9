package ninep

import (
	"encoding/binary"
	"fmt"
	"io"
)

func ParseDirEntries(data []byte) ([]Dir, error) {
	dirs := make([]Dir, 0)
	for len(data) > 0 {
		if len(data) < 2 {
			return nil, io.ErrUnexpectedEOF
		}
		size := int(binary.LittleEndian.Uint16(data[:2]))
		if len(data) < 2+size {
			return nil, fmt.Errorf("truncated dir entry: need %d bytes, have %d", 2+size, len(data))
		}
		dir, err := unmarshalDir(data[:2+size])
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, *dir)
		data = data[2+size:]
	}
	return dirs, nil
}

func EncodeDir(dir *Dir) ([]byte, error) {
	return marshalDir(dir)
}
