package ninep

import "time"

const (
	TVERSION uint8  = 100
	RVERSION uint8  = 101
	TATTACH  uint8  = 104
	RATTACH  uint8  = 105
	TWALK    uint8  = 110
	RWALK    uint8  = 111
	TOPEN    uint8  = 112
	ROPEN    uint8  = 113
	TCREATE  uint8  = 114
	RCREATE  uint8  = 115
	TREAD    uint8  = 116
	RREAD    uint8  = 117
	TWRITE   uint8  = 118
	RWRITE   uint8  = 119
	TCLUNK   uint8  = 120
	RCLUNK   uint8  = 121
	TREMOVE  uint8  = 122
	RREMOVE  uint8  = 123
	TSTAT    uint8  = 124
	RSTAT    uint8  = 125
	TWSTAT   uint8  = 126
	RWSTAT   uint8  = 127
	RERROR   uint8  = 107
	NOFID    uint32 = ^uint32(0)
	NOTAG    uint16 = ^uint16(0)
)

type QID struct {
	Type    uint8
	Version uint32
	Path    uint64
}

type Dir struct {
	Type   uint16
	Dev    uint32
	QID    QID
	Mode   uint32
	Atime  uint32
	Mtime  uint32
	Length uint64
	Name   string
	UID    string
	GID    string
	MUID   string
}

func (d Dir) ModTime() time.Time {
	return time.Unix(int64(d.Mtime), 0)
}

type Fcall struct {
	Type    uint8
	Tag     uint16
	Msize   uint32
	Version string
	FID     uint32
	NewFID  uint32
	AFID    uint32
	UNAME   string
	ANAME   string
	WNames  []string
	WQIDs   []QID
	Mode    uint8
	Name    string
	Perm    uint32
	Offset  uint64
	Count   uint32
	Data    []byte
	QID     QID
	Iounit  uint32
	Dir     *Dir
	Ename   string
}
