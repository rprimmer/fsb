package guard

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"strings"
	"time"
)

// A zip lists its members in a central directory at its end. archive/zip reads
// all of it into memory before anything can be counted, and does not stop at
// the size the archive declares for it, so a valid archive with millions of
// entries costs memory in proportion. listZip reads the directory itself, one
// record at a time, keeping only what it shows, and stops at the entry and
// byte budgets: what it costs is bounded whatever the archive claims.

var le = binary.LittleEndian

const (
	sigEOCD       = 0x06054b50
	sigEOCD64     = 0x06064b50
	sigEOCD64Loc  = 0x07064b50
	sigDirRecord  = 0x02014b50
	eocdLen       = 22
	eocd64LocLen  = 20
	eocd64Len     = 56
	dirRecordLen  = 46
	maxCommentLen = 65535
)

func listZip(ctx context.Context, f io.ReaderAt, size int64) (ArchiveListing, error) {
	if err := ctx.Err(); err != nil {
		return ArchiveListing{}, err
	}
	start, dirSize, count, zip64, err := findZipDir(f, size)
	if err != nil {
		return ArchiveListing{}, err
	}
	l := ArchiveListing{Format: "zip"}
	// A damaged record after some good ones: report what we have, as for tar.
	damaged := func() (ArchiveListing, error) {
		if l.Total == 0 {
			return ArchiveListing{}, ErrUnsupported
		}
		l.Incomplete = true
		return l, nil
	}
	r := bufio.NewReader(io.NewSectionReader(f, start, dirSize))
	var read int64
	var rec [dirRecordLen]byte
	for read < dirSize {
		if l.Total%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return ArchiveListing{}, err
			}
		}
		if l.Total >= archiveMaxScanned || read >= archiveMaxZipDir {
			l.Incomplete = true
			return l, nil
		}
		if _, err := io.ReadFull(r, rec[:]); err != nil || le.Uint32(rec[0:]) != sigDirRecord {
			return damaged()
		}
		nameLen, extraLen, commentLen := int(le.Uint16(rec[28:])), int(le.Uint16(rec[30:])), int(le.Uint16(rec[32:]))
		read += int64(dirRecordLen + nameLen + extraLen + commentLen)
		if read > dirSize {
			return damaged()
		}
		if len(l.Entries) >= archiveMaxShown {
			l.Truncated = true
			if _, err := r.Discard(nameLen + extraLen + commentLen); err != nil {
				return damaged()
			}
			l.Total++
			continue
		}
		buf := make([]byte, nameLen+extraLen)
		if _, err := io.ReadFull(r, buf); err != nil {
			return damaged()
		}
		if _, err := r.Discard(commentLen); err != nil {
			return damaged()
		}
		name, extra := string(buf[:nameLen]), buf[nameLen:]
		l.Entries = append(l.Entries, ArchiveEntry{
			Name:    cleanName(name),
			Size:    int64(min(zipSize(rec[:], extra), 1<<62)),
			IsDir:   strings.HasSuffix(name, "/"),
			ModTime: zipModTime(rec[:], extra),
		})
		l.Total++
	}
	// The whole directory was read, so its count must agree with the archive's
	// (whose 16-bit field wraps in archives too big for it that are not ZIP64).
	if (zip64 && uint64(l.Total) != count) || (!zip64 && uint16(l.Total) != uint16(count)) {
		return ArchiveListing{}, ErrUnsupported
	}
	return l, nil
}

// findZipDir locates the central directory from the end-of-directory record
// (and its ZIP64 counterpart). The directory is taken to end where that record
// begins, so data in front of the archive (a self-extracting stub) is no matter.
func findZipDir(f io.ReaderAt, size int64) (start, dirSize int64, count uint64, zip64 bool, err error) {
	tail := min(size, eocdLen+maxCommentLen)
	buf := make([]byte, tail)
	if _, err := f.ReadAt(buf, size-tail); err != nil && err != io.EOF {
		return 0, 0, 0, false, mapErr(err)
	}
	i := -1
	for j := len(buf) - eocdLen; j >= 0; j-- {
		if le.Uint32(buf[j:]) == sigEOCD && j+eocdLen+int(le.Uint16(buf[j+20:])) <= len(buf) {
			i = j
			break
		}
	}
	if i < 0 {
		return 0, 0, 0, false, ErrUnsupported
	}
	e := buf[i:]
	eocdPos := size - tail + int64(i)
	count, size32, dirEnd := uint64(le.Uint16(e[10:])), uint64(le.Uint32(e[12:])), eocdPos
	dirSize64 := size32
	// A ZIP64 locator just before the record means ZIP64 end records sit
	// between the directory and here, whether or not the ordinary record's
	// fields overflowed (Info-ZIP writing to a pipe adds them regardless).
	{
		var loc [eocd64LocLen]byte
		if eocdPos >= eocd64LocLen {
			if _, err := f.ReadAt(loc[:], eocdPos-eocd64LocLen); err == nil && le.Uint32(loc[:]) == sigEOCD64Loc {
				recPos := le.Uint64(loc[8:])
				var rec [eocd64Len]byte
				if recPos > uint64(eocdPos-eocd64LocLen-eocd64Len) {
					return 0, 0, 0, false, ErrUnsupported
				}
				if _, err := f.ReadAt(rec[:], int64(recPos)); err != nil || le.Uint32(rec[:]) != sigEOCD64 {
					return 0, 0, 0, false, ErrUnsupported
				}
				count, dirSize64, dirEnd, zip64 = le.Uint64(rec[32:]), le.Uint64(rec[40:]), int64(recPos), true
			}
		}
	}
	if dirSize64 > uint64(dirEnd) {
		return 0, 0, 0, false, ErrUnsupported
	}
	return dirEnd - int64(dirSize64), int64(dirSize64), count, zip64, nil
}

// zipSize is a record's uncompressed size, from the ZIP64 extra field when the
// 32-bit field says it is there.
func zipSize(rec, extra []byte) uint64 {
	n := uint64(le.Uint32(rec[24:]))
	if n != 0xFFFFFFFF {
		return n
	}
	if d := zipExtra(extra, 0x0001); len(d) >= 8 {
		return le.Uint64(d) // the uncompressed size comes first
	}
	return n
}

// zipModTime is a record's modification time: the Unix time of the extended
// timestamp field if present, else the MS-DOS date and time (2-second steps,
// no time zone, so labeled UTC as archive/zip does).
func zipModTime(rec, extra []byte) time.Time {
	if d := zipExtra(extra, 0x5455); len(d) >= 5 && d[0]&1 != 0 {
		return time.Unix(int64(le.Uint32(d[1:])), 0).UTC() // unsigned, as archive/zip reads it (dates past 2038)
	}
	t, d := le.Uint16(rec[12:]), le.Uint16(rec[14:])
	if d == 0 {
		return time.Time{}
	}
	return time.Date(1980+int(d>>9), time.Month(d>>5&0xf), int(d&0x1f), int(t>>11), int(t>>5&0x3f), int(t&0x1f)*2, 0, time.UTC)
}

// zipExtra returns the data of the first extra field with the given tag.
func zipExtra(extra []byte, tag uint16) []byte {
	for len(extra) >= 4 {
		id, n := le.Uint16(extra), int(le.Uint16(extra[2:]))
		if 4+n > len(extra) {
			return nil
		}
		if id == tag {
			return extra[4 : 4+n]
		}
		extra = extra[4+n:]
	}
	return nil
}
