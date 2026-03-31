package ninep

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

func Marshal(f Fcall) ([]byte, error) {
	var body bytes.Buffer
	write8(&body, f.Type)
	write16(&body, f.Tag)
	switch f.Type {
	case TVERSION, RVERSION:
		write32(&body, f.Msize)
		writeString(&body, f.Version)
	case TATTACH:
		write32(&body, f.FID)
		write32(&body, f.AFID)
		writeString(&body, f.UNAME)
		writeString(&body, f.ANAME)
	case RATTACH:
		writeQID(&body, f.QID)
	case TWALK:
		write32(&body, f.FID)
		write32(&body, f.NewFID)
		write16(&body, uint16(len(f.WNames)))
		for _, name := range f.WNames {
			writeString(&body, name)
		}
	case RWALK:
		write16(&body, uint16(len(f.WQIDs)))
		for _, qid := range f.WQIDs {
			writeQID(&body, qid)
		}
	case TOPEN:
		write32(&body, f.FID)
		write8(&body, f.Mode)
	case ROPEN:
		writeQID(&body, f.QID)
		write32(&body, f.Iounit)
	case TCREATE:
		write32(&body, f.FID)
		writeString(&body, f.Name)
		write32(&body, f.Perm)
		write8(&body, f.Mode)
	case RCREATE:
		writeQID(&body, f.QID)
		write32(&body, f.Iounit)
	case TREAD:
		write32(&body, f.FID)
		write64(&body, f.Offset)
		write32(&body, f.Count)
	case RREAD:
		write32(&body, uint32(len(f.Data)))
		body.Write(f.Data)
	case TWRITE:
		write32(&body, f.FID)
		write64(&body, f.Offset)
		write32(&body, uint32(len(f.Data)))
		body.Write(f.Data)
	case RWRITE:
		write32(&body, f.Count)
	case TCLUNK, TREMOVE, TSTAT:
		write32(&body, f.FID)
	case TWSTAT:
		write32(&body, f.FID)
		dir, err := marshalDir(f.Dir)
		if err != nil {
			return nil, err
		}
		write16(&body, uint16(len(dir)))
		body.Write(dir)
	case RSTAT:
		dir, err := marshalDir(f.Dir)
		if err != nil {
			return nil, err
		}
		write16(&body, uint16(len(dir)))
		body.Write(dir)
	case RCLUNK, RREMOVE, RWSTAT:
	case RERROR:
		writeString(&body, f.Ename)
	default:
		return nil, fmt.Errorf("unsupported fcall type %d", f.Type)
	}
	out := make([]byte, 4+body.Len())
	binary.LittleEndian.PutUint32(out[:4], uint32(len(out)))
	copy(out[4:], body.Bytes())
	return out, nil
}

func Unmarshal(data []byte) (Fcall, error) {
	if len(data) < 7 {
		return Fcall{}, io.ErrUnexpectedEOF
	}
	if int(binary.LittleEndian.Uint32(data[:4])) != len(data) {
		return Fcall{}, fmt.Errorf("invalid size %d", len(data))
	}
	r := bytes.NewReader(data[4:])
	var f Fcall
	f.Type = read8(r)
	f.Tag = read16(r)
	switch f.Type {
	case TVERSION, RVERSION:
		f.Msize = read32(r)
		f.Version = readString(r)
	case TATTACH:
		f.FID = read32(r)
		f.AFID = read32(r)
		f.UNAME = readString(r)
		f.ANAME = readString(r)
	case RATTACH:
		f.QID = readQID(r)
	case TWALK:
		f.FID = read32(r)
		f.NewFID = read32(r)
		n := int(read16(r))
		f.WNames = make([]string, n)
		for i := 0; i < n; i++ {
			f.WNames[i] = readString(r)
		}
	case RWALK:
		n := int(read16(r))
		f.WQIDs = make([]QID, n)
		for i := 0; i < n; i++ {
			f.WQIDs[i] = readQID(r)
		}
	case TOPEN:
		f.FID = read32(r)
		f.Mode = read8(r)
	case ROPEN, RCREATE:
		f.QID = readQID(r)
		f.Iounit = read32(r)
	case TCREATE:
		f.FID = read32(r)
		f.Name = readString(r)
		f.Perm = read32(r)
		f.Mode = read8(r)
	case TREAD:
		f.FID = read32(r)
		f.Offset = read64(r)
		f.Count = read32(r)
	case RREAD:
		n := int(read32(r))
		f.Data = make([]byte, n)
		if _, err := io.ReadFull(r, f.Data); err != nil {
			return Fcall{}, err
		}
	case TWRITE:
		f.FID = read32(r)
		f.Offset = read64(r)
		n := int(read32(r))
		f.Data = make([]byte, n)
		if _, err := io.ReadFull(r, f.Data); err != nil {
			return Fcall{}, err
		}
	case RWRITE:
		f.Count = read32(r)
	case TCLUNK, TREMOVE, TSTAT:
		f.FID = read32(r)
	case TWSTAT, RSTAT:
		if f.Type == TWSTAT {
			f.FID = read32(r)
		}
		n := int(read16(r))
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return Fcall{}, err
		}
		dir, err := unmarshalDir(buf)
		if err != nil {
			return Fcall{}, err
		}
		f.Dir = dir
	case RERROR:
		f.Ename = readString(r)
	case RCLUNK, RREMOVE, RWSTAT:
	default:
		return Fcall{}, fmt.Errorf("unsupported fcall type %d", f.Type)
	}
	return f, nil
}

func marshalDir(d *Dir) ([]byte, error) {
	if d == nil {
		return nil, nil
	}
	var body bytes.Buffer
	write16(&body, 0)
	write16(&body, d.Type)
	write32(&body, d.Dev)
	writeQID(&body, d.QID)
	write32(&body, d.Mode)
	write32(&body, d.Atime)
	write32(&body, d.Mtime)
	write64(&body, d.Length)
	writeString(&body, d.Name)
	writeString(&body, d.UID)
	writeString(&body, d.GID)
	writeString(&body, d.MUID)
	buf := body.Bytes()
	binary.LittleEndian.PutUint16(buf[:2], uint16(len(buf)-2))
	return buf, nil
}

func unmarshalDir(buf []byte) (*Dir, error) {
	r := bytes.NewReader(buf)
	_ = read16(r)
	d := &Dir{}
	d.Type = read16(r)
	d.Dev = read32(r)
	d.QID = readQID(r)
	d.Mode = read32(r)
	d.Atime = read32(r)
	d.Mtime = read32(r)
	d.Length = read64(r)
	d.Name = readString(r)
	d.UID = readString(r)
	d.GID = readString(r)
	d.MUID = readString(r)
	return d, nil
}

func write8(w *bytes.Buffer, v uint8)       { _ = w.WriteByte(v) }
func write16(w *bytes.Buffer, v uint16)     { _ = binary.Write(w, binary.LittleEndian, v) }
func write32(w *bytes.Buffer, v uint32)     { _ = binary.Write(w, binary.LittleEndian, v) }
func write64(w *bytes.Buffer, v uint64)     { _ = binary.Write(w, binary.LittleEndian, v) }
func writeString(w *bytes.Buffer, s string) { write16(w, uint16(len(s))); _, _ = w.WriteString(s) }
func writeQID(w *bytes.Buffer, q QID)       { write8(w, q.Type); write32(w, q.Version); write64(w, q.Path) }
func read8(r io.Reader) uint8               { var v [1]byte; _, _ = io.ReadFull(r, v[:]); return v[0] }
func read16(r io.Reader) uint16             { var v uint16; _ = binary.Read(r, binary.LittleEndian, &v); return v }
func read32(r io.Reader) uint32             { var v uint32; _ = binary.Read(r, binary.LittleEndian, &v); return v }
func read64(r io.Reader) uint64             { var v uint64; _ = binary.Read(r, binary.LittleEndian, &v); return v }
func readString(r io.Reader) string {
	n := int(read16(r))
	buf := make([]byte, n)
	_, _ = io.ReadFull(r, buf)
	return string(buf)
}
func readQID(r io.Reader) QID { return QID{Type: read8(r), Version: read32(r), Path: read64(r)} }
