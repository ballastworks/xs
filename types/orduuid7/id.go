package orduuid7

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"time"
)

const (
	byteCount          = 16
	marshaledByteCount = byteCount * 2
)

var (
	ErrInvalidUnmarshalText     = errors.New("OrdUuid7: invalid unmarshal text")
	ErrInvalidJsonUnmarshalText = errors.New("OrdUuid7: invalid JSON unmarshal text")
	ErrInvalidScanValue         = errors.New("OrdUuid7: invalid scan value")
	ErrCannotMarshalValue       = errors.New("OrdUuid7: cannot marshal value")
)

type OrdUuid7 [byteCount]byte

func New() OrdUuid7 {
	return new(time.Now().UnixMilli())
}

func new(ms int64) OrdUuid7 {
	var id OrdUuid7

	id[0] = byte(ms >> (8 * 5))
	id[1] = byte(ms >> (8 * 4))
	id[2] = byte(ms >> (8 * 3))
	id[3] = byte(ms >> (8 * 2))
	id[4] = byte(ms >> (8 * 1))
	id[5] = byte(ms)

	rand.Read(id[6:]) // note: crypto/rand.Read never errors now

	id[15] = (id[15] & 0xC0) | 0x27

	return id
}

func (id OrdUuid7) ValidNonZero() bool {
	return (id[15] & 0x3F) == 0x27
}

func (id OrdUuid7) appendText(b []byte) []byte {
	return hex.AppendEncode(b, id[:])
}

func (id OrdUuid7) AppendText(b []byte) ([]byte, error) {
	if !id.ValidNonZero() {
		return nil, ErrCannotMarshalValue
	}

	return id.appendText(b), nil
}

func (id OrdUuid7) MarshalText() ([]byte, error) {
	if !id.ValidNonZero() {
		return nil, ErrCannotMarshalValue
	}

	var buf [marshaledByteCount]byte
	return id.appendText(buf[:0]), nil
}

func (id *OrdUuid7) UnmarshalText(p []byte) error {
	var buf OrdUuid7
	if len(p) != marshaledByteCount {
		return ErrInvalidUnmarshalText
	}

	_, err := hex.Decode(buf[:], p)
	if err != nil {
		return errors.Join(ErrInvalidUnmarshalText, err)
	}

	if !buf.ValidNonZero() {
		return ErrInvalidUnmarshalText
	}

	*id = buf
	return nil
}

func (id OrdUuid7) MarshalJSON() ([]byte, error) {
	if !id.ValidNonZero() {
		return nil, ErrCannotMarshalValue
	}

	var buf [marshaledByteCount + 2]byte
	id.appendText(buf[1:1])
	buf[0] = '"'
	buf[len(buf)-1] = '"'

	return buf[:], nil
}

func (id *OrdUuid7) UnmarshalJSON(p []byte) error {
	if string(p) == "null" {
		return nil
	}

	if len(p) != marshaledByteCount+2 || p[0] != '"' || p[len(p)-1] != '"' {
		return ErrInvalidJsonUnmarshalText
	}

	var v OrdUuid7
	if err := v.UnmarshalText(p[1 : len(p)-1]); err != nil {
		return ErrInvalidJsonUnmarshalText
	}

	*id = v
	return nil
}

func (id OrdUuid7) String() string {
	b, err := id.MarshalText()
	if err != nil {
		return ""
	}

	return string(b)
}

func (id OrdUuid7) Value() (driver.Value, error) {
	if !id.ValidNonZero() {
		return nil, ErrCannotMarshalValue
	}

	return id[:], nil
}

func (id *OrdUuid7) Scan(v any) error {
	var buf OrdUuid7

	switch vt := v.(type) {
	case string:
		if len(vt) != byteCount {
			return ErrInvalidScanValue
		}

		copy(buf[:], []byte(vt))
	case []byte:
		if len(vt) != byteCount {
			return ErrInvalidScanValue
		}

		copy(buf[:], vt)
	default:
		return ErrInvalidScanValue
	}

	if !buf.ValidNonZero() {
		return ErrInvalidScanValue
	}

	*id = buf
	return nil
}
