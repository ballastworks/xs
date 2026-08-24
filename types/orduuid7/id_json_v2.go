//go:build go1.27

package orduuid7

import (
	"encoding/json/jsontext"
	"errors"
)

var (
	ErrCannotUnmarshalNullJsonValue = errors.New("OrdUuid7: cannot unmarshal null json value")
)

func (id OrdUuid7) MarshalJSONTo(enc *jsontext.Encoder) error {
	if !id.ValidNonZero() {
		return ErrCannotMarshalValue
	}

	var buf [marshaledByteCount + 2]byte
	buf[0] = '"'
	id.appendText(buf[1:1])
	buf[len(buf)-1] = '"'

	return enc.WriteValue(buf[:])
}

func (id *OrdUuid7) UnmarshalJSONFrom(dec *jsontext.Decoder) error {

	switch dec.PeekKind() {
	case 0:
		_, err := dec.ReadToken()
		return err
	case 'n':
		return ErrCannotUnmarshalNullJsonValue
	case '"':
		break
	default:
		return ErrInvalidJsonUnmarshalText
	}

	jv, err := dec.ReadValue()
	if err != nil {
		return err
	}

	if len(jv) == marshaledByteCount+2 {
		return id.UnmarshalText(jv[1 : len(jv)-1])
	}

	if len(jv) < marshaledByteCount+2 || len(jv) > marshaledByteCount*6+2 {
		return ErrInvalidJsonUnmarshalText
	}

	var rawBuf [marshaledByteCount * 6]byte
	buf, err := jsontext.AppendUnquote(rawBuf[:0], jv)
	if err != nil {
		return err
	}

	return id.UnmarshalText(buf)
}
