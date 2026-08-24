package orduuid7

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/josephcopenhaver/tbdd-go"
	"github.com/stretchr/testify/assert"
)

// jsonEscapeHex renders s as the contents of a JSON string using
// backslash-u00XX escapes so decoders take the unquote path rather
// than the compact fast path.
func jsonEscapeHex(s string) string {
	const hexDigits = "0123456789abcdef"

	var sb strings.Builder
	for i := range len(s) {
		sb.WriteByte('\\')
		sb.WriteString("u00")
		sb.WriteByte(hexDigits[s[i]>>4])
		sb.WriteByte(hexDigits[s[i]&0x0F])
	}
	return sb.String()
}

// verify that the type implements expected interfaces
var (
	_ fmt.Stringer = OrdUuid7{}

	_ encoding.TextAppender    = OrdUuid7{}
	_ encoding.TextMarshaler   = OrdUuid7{}
	_ encoding.TextUnmarshaler = &OrdUuid7{}

	_ json.Marshaler   = OrdUuid7{}
	_ json.Unmarshaler = &OrdUuid7{}

	_ driver.Valuer = OrdUuid7{}
	_ sql.Scanner   = &OrdUuid7{}
)

func TestNew(t *testing.T) {
	t.Parallel()

	type TC struct{}
	type R struct {
		beforeMs int64
		afterMs  int64
		id       OrdUuid7
	}

	tbdd.WT(
		TC{},
		"creating a new id",
		func(t *testing.T, tc TC) R {
			beforeMs := time.Now().UnixMilli()
			id := New()
			afterMs := time.Now().UnixMilli()

			return R{beforeMs, afterMs, id}
		},
		"it is valid-non-zero and leads with the current unix millisecond timestamp in big-endian order",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.True(r.id.ValidNonZero())

			var ms int64
			for _, b := range r.id[:6] {
				ms = (ms << 8) | int64(b)
			}
			is.GreaterOrEqual(ms, r.beforeMs)
			is.LessOrEqual(ms, r.afterMs)
		},
	).Run(t)
}

func TestNewEncoding(t *testing.T) {
	t.Parallel()

	const ms = int64(0x010203040506)

	type TC struct{}
	type R struct {
		a, b OrdUuid7
	}

	tbdd.WT(
		TC{},
		"creating two ids from the same millisecond timestamp",
		func(t *testing.T, tc TC) R {
			return R{new(ms), new(ms)}
		},
		"both encode the timestamp bytes and validity marker but differ in their random section",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			for _, id := range []OrdUuid7{r.a, r.b} {
				is.Equal([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}, id[:6])
				is.Equal(byte(0x27), id[15]&0x3F)
				is.True(id.ValidNonZero())
			}

			is.NotEqual(r.a[6:], r.b[6:])
		},
	).Run(t)
}

func TestOrdering(t *testing.T) {
	t.Parallel()

	type TC struct {
		earlierMs, laterMs int64
	}
	type R struct {
		earlier, later OrdUuid7
	}

	lc := tbdd.WT(
		TC{earlierMs: 0x010203040506, laterMs: 0x010203040507},
		"creating ids from increasing millisecond timestamps",
		func(t *testing.T, tc TC) R {
			return R{new(tc.earlierMs), new(tc.laterMs)}
		},
		"they sort lexicographically by creation time",
		func(t *testing.T, tc TC, r R) {
			assert.New(t).Negative(bytes.Compare(r.earlier[:], r.later[:]))
		},
	)

	lc.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
		return func(yield func(tbdd.TestVariant[TC]) bool) {
			yield(tbdd.TestVariant[TC]{
				Kind: "full-timestamp-range",
				TC:   TC{earlierMs: 0, laterMs: 0xFFFFFFFFFFFF},
			})
		}
	}

	lc.Run(t)
}

func TestValidNonZero(t *testing.T) {
	t.Parallel()

	type TC struct {
		id  OrdUuid7
		exp bool
	}
	type R struct {
		valid bool
	}

	lc := tbdd.WT(
		TC{id: New(), exp: true},
		"checking validity",
		func(t *testing.T, tc TC) R {
			return R{tc.id.ValidNonZero()}
		},
		"it reports whether the low six bits of the final byte match the marker",
		func(t *testing.T, tc TC, r R) {
			assert.New(t).Equal(tc.exp, r.valid)
		},
	)

	lc.Variants = func(_ *testing.T, tc TC) iter.Seq[tbdd.TestVariant[TC]] {
		return func(yield func(tbdd.TestVariant[TC]) bool) {
			zero := TC{exp: false}
			if !yield(tbdd.TestVariant[TC]{Kind: "zero-value", TC: zero}) {
				return
			}

			corrupted := tc
			corrupted.id[15] ^= 0x01
			corrupted.exp = false
			if !yield(tbdd.TestVariant[TC]{Kind: "corrupted-marker", TC: corrupted}) {
				return
			}

			// the top two bits of the final byte are random, not marker
			highBits := tc
			highBits.id[15] ^= 0xC0
			highBits.exp = true
			yield(tbdd.TestVariant[TC]{Kind: "flipped-random-high-bits", TC: highBits})
		}
	}

	lc.Run(t)
}

func TestMarshalText(t *testing.T) {
	t.Parallel()

	type TC struct {
		id OrdUuid7
	}
	type R struct {
		appended   []byte
		appendErr  error
		marshaled  []byte
		marshalErr error
		str        string
	}

	tbdd.WT(
		TC{id: New()},
		"marshaling to text via AppendText, MarshalText, and String",
		func(t *testing.T, tc TC) R {
			appended, appendErr := tc.id.AppendText([]byte("prefix:"))
			marshaled, marshalErr := tc.id.MarshalText()

			return R{appended, appendErr, marshaled, marshalErr, tc.id.String()}
		},
		"all render the same 32 lowercase hex characters and AppendText preserves its prefix",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			exp := hex.EncodeToString(tc.id[:])

			is.NoError(r.appendErr)
			is.Equal("prefix:"+exp, string(r.appended))

			is.NoError(r.marshalErr)
			is.Len(r.marshaled, marshaledByteCount)
			is.Equal(exp, string(r.marshaled))

			is.Equal(exp, r.str)
		},
	).Run(t)
}

func TestMarshalInvalid(t *testing.T) {
	t.Parallel()

	type TC struct {
		id OrdUuid7
	}
	type R struct {
		appended      []byte
		appendErr     error
		marshaled     []byte
		marshalErr    error
		jsonDirect    []byte
		jsonDirectErr error
		viaEncoderErr error
		str           string
		value         driver.Value
		valueErr      error
	}

	lc := tbdd.WT(
		TC{},
		"marshaling an invalid id through every marshal path",
		func(t *testing.T, tc TC) R {
			appended, appendErr := tc.id.AppendText([]byte("prefix:"))
			marshaled, marshalErr := tc.id.MarshalText()
			jsonDirect, jsonDirectErr := tc.id.MarshalJSON()
			_, viaEncoderErr := json.Marshal(tc.id)
			value, valueErr := tc.id.Value()

			return R{appended, appendErr, marshaled, marshalErr, jsonDirect, jsonDirectErr, viaEncoderErr, tc.id.String(), value, valueErr}
		},
		"each errors with ErrCannotMarshalValue and String returns an empty string",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.ErrorIs(r.appendErr, ErrCannotMarshalValue)
			is.Nil(r.appended)

			is.ErrorIs(r.marshalErr, ErrCannotMarshalValue)
			is.Nil(r.marshaled)

			is.ErrorIs(r.jsonDirectErr, ErrCannotMarshalValue)
			is.Nil(r.jsonDirect)

			is.ErrorIs(r.viaEncoderErr, ErrCannotMarshalValue)

			is.ErrorIs(r.valueErr, ErrCannotMarshalValue)
			is.Nil(r.value)

			is.Empty(r.str)
		},
	)

	lc.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
		return func(yield func(tbdd.TestVariant[TC]) bool) {
			corrupted := New()
			corrupted[15] ^= 0x01

			yield(tbdd.TestVariant[TC]{Kind: "corrupted-marker", TC: TC{id: corrupted}})
		}
	}

	lc.Run(t)
}

func TestUnmarshalText(t *testing.T) {
	t.Parallel()

	src := New()

	{
		type TC struct {
			text string
		}
		type R struct {
			id  OrdUuid7
			err error
		}

		tbdd.WT(
			TC{text: src.String()},
			"unmarshaling the text of a valid id",
			func(t *testing.T, tc TC) R {
				var id OrdUuid7
				err := id.UnmarshalText([]byte(tc.text))

				return R{id, err}
			},
			"it round-trips without error",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.NoError(r.err)
				is.Equal(src, r.id)
			},
		).Run(t)
	}

	{
		type TC struct {
			text      string
			expHexErr bool
		}
		type R struct {
			id  OrdUuid7
			err error
		}

		lc := tbdd.WT(
			TC{text: src.String()[:marshaledByteCount-1]},
			"unmarshaling invalid text into an already-populated id",
			func(t *testing.T, tc TC) R {
				id := src
				err := id.UnmarshalText([]byte(tc.text))

				return R{id, err}
			},
			"it errors with ErrInvalidUnmarshalText and leaves the destination unchanged",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.ErrorIs(r.err, ErrInvalidUnmarshalText)
				if tc.expHexErr {
					var hexErr hex.InvalidByteError
					is.ErrorAs(r.err, &hexErr)
				}
				is.Equal(src, r.id)
			},
		)

		lc.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
			return func(yield func(tbdd.TestVariant[TC]) bool) {
				for _, v := range []tbdd.TestVariant[TC]{
					{Kind: "empty", TC: TC{text: ""}},
					{Kind: "too-long", TC: TC{text: src.String() + "00"}},
					{Kind: "invalid-hex", TC: TC{text: strings.Repeat("zz", byteCount), expHexErr: true}},
					{Kind: "valid-hex-invalid-marker", TC: TC{text: strings.Repeat("00", byteCount)}},
				} {
					if !yield(v) {
						return
					}
				}
			}
		}

		lc.Run(t)
	}
}

func TestMarshalJSON(t *testing.T) {
	t.Parallel()

	type TC struct {
		id OrdUuid7
	}
	type R struct {
		direct        []byte
		directErr     error
		viaEncoder    []byte
		viaEncoderErr error
	}

	tbdd.WT(
		TC{id: New()},
		"marshaling to JSON directly and through the json package",
		func(t *testing.T, tc TC) R {
			direct, directErr := tc.id.MarshalJSON()
			viaEncoder, viaEncoderErr := json.Marshal(tc.id)

			return R{direct, directErr, viaEncoder, viaEncoderErr}
		},
		"both produce the double-quoted hex string",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			exp := `"` + tc.id.String() + `"`

			is.NoError(r.directErr)
			is.Equal(exp, string(r.direct))

			is.NoError(r.viaEncoderErr)
			is.Equal(exp, string(r.viaEncoder))
		},
	).Run(t)
}

func TestUnmarshalJSON(t *testing.T) {
	t.Parallel()

	src := New()

	{
		type TC struct {
			text string
		}
		type R struct {
			id  OrdUuid7
			err error
		}

		tbdd.WT(
			TC{text: `"` + src.String() + `"`},
			"unmarshaling the JSON of a valid id",
			func(t *testing.T, tc TC) R {
				var id OrdUuid7
				err := id.UnmarshalJSON([]byte(tc.text))

				return R{id, err}
			},
			"it round-trips without error",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.NoError(r.err)
				is.Equal(src, r.id)
			},
		).Run(t)

		tbdd.WT(
			TC{text: "null"},
			"unmarshaling the JSON null literal into a populated id",
			func(t *testing.T, tc TC) R {
				id := src
				err := id.UnmarshalJSON([]byte(tc.text))

				return R{id, err}
			},
			"it is a no-op",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.NoError(r.err)
				is.Equal(src, r.id)
			},
		).Run(t)

		tbdd.WT(
			TC{text: `"` + jsonEscapeHex(`00000000000000000000000000000027`) + `"`},
			"unmarshaling the JSON inflated literal into a populated id",
			func(t *testing.T, tc TC) R {
				id := src
				err := id.UnmarshalJSON([]byte(tc.text))

				return R{id, err}
			},
			"sets the ID to the deflated value",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.NoError(r.err)
				is.NotEqual(src, r.id)
				for i := range byteCount - 1 {
					is.Equal(r.id[i], uint8(0))
				}
				is.True(r.id.ValidNonZero())
			},
		).Run(t)
	}

	{
		type TC struct {
			text string
		}
		type R struct {
			id  OrdUuid7
			err error
		}

		lc := tbdd.WT(
			TC{text: src.String()},
			"unmarshaling invalid JSON into an already-populated id",
			func(t *testing.T, tc TC) R {
				id := src
				err := id.UnmarshalJSON([]byte(tc.text))

				return R{id, err}
			},
			"it errors with only ErrInvalidJsonUnmarshalText and leaves the destination unchanged",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.ErrorIs(r.err, ErrInvalidJsonUnmarshalText)
				is.NotErrorIs(r.err, ErrInvalidUnmarshalText)
				is.Equal(src, r.id)
			},
		)

		lc.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
			return func(yield func(tbdd.TestVariant[TC]) bool) {
				for _, v := range []tbdd.TestVariant[TC]{
					{Kind: "empty", TC: TC{text: ""}},
					{Kind: "too-short", TC: TC{text: `"` + src.String()[:marshaledByteCount-1] + `"`}},
					{Kind: "too-long", TC: TC{text: `"` + src.String() + `0"`}},
					{Kind: "missing-leading-quote", TC: TC{text: "0" + src.String() + `"`}},
					{Kind: "missing-trailing-quote", TC: TC{text: `"` + src.String() + "0"}},
					{Kind: "invalid-hex", TC: TC{text: `"` + strings.Repeat("zz", byteCount) + `"`}},
					{Kind: "valid-hex-invalid-marker", TC: TC{text: `"` + strings.Repeat("00", byteCount) + `"`}},
					{Kind: "escaped-invalid-hex", TC: TC{text: `"` + jsonEscapeHex("z") + strings.Repeat("z", marshaledByteCount-1) + `"`}},
					{Kind: "escaped-valid-hex-invalid-marker", TC: TC{text: `"` + jsonEscapeHex("0") + strings.Repeat("0", marshaledByteCount-1) + `"`}},
				} {
					if !yield(v) {
						return
					}
				}
			}
		}

		lc.Run(t)
	}

	{
		type TC struct {
			text string
		}
		type R struct {
			id  OrdUuid7
			err error
		}

		tbdd.WT(
			// jsonEscapeHex("0")[:2] is a lone `\u` prefix; following it
			// with non-hex characters makes the escape sequence malformed
			// while the length and quote checks still pass
			TC{text: `"` + jsonEscapeHex("0")[:2] + strings.Repeat("z", marshaledByteCount+1) + `"`},
			"unmarshaling a JSON string with a malformed escape sequence into a populated id",
			func(t *testing.T, tc TC) R {
				id := src
				err := id.UnmarshalJSON([]byte(tc.text))

				return R{id, err}
			},
			"it surfaces the json decoding error and leaves the destination unchanged",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.Error(r.err)
				is.NotErrorIs(r.err, ErrInvalidJsonUnmarshalText)
				is.Equal(src, r.id)
			},
		).Run(t)
	}
}

func TestBytes(t *testing.T) {
	t.Parallel()

	type TC struct {
		id OrdUuid7
	}
	type R struct {
		b []byte
	}

	tbdd.WT(
		TC{id: New()},
		"getting Bytes and mutating the returned slice",
		func(t *testing.T, tc TC) R {
			b := tc.id.Bytes()
			b[0] ^= 0xFF

			return R{b}
		},
		"the slice holds all 16 bytes but is a copy detached from the id",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.Len(r.b, byteCount)
			is.Equal(tc.id[0]^0xFF, r.b[0])
			is.Equal(tc.id[1:], r.b[1:])
		},
	).Run(t)
}

func TestMutBytes(t *testing.T) {
	t.Parallel()

	type TC struct {
		id OrdUuid7
	}
	type R struct {
		id OrdUuid7
		b  []byte
	}

	tbdd.WT(
		TC{id: New()},
		"getting MutBytes and mutating the returned slice",
		func(t *testing.T, tc TC) R {
			id := tc.id
			b := id.MutBytes()
			b[0] ^= 0xFF

			return R{id, b}
		},
		"the slice holds all 16 bytes and aliases the id so the mutation is visible",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.Len(r.b, byteCount)
			is.Equal(tc.id[0]^0xFF, r.id[0])
			is.Equal(tc.id[1:], r.id[1:])
			is.Equal(r.id[:], r.b)
		},
	).Run(t)
}

func TestValue(t *testing.T) {
	t.Parallel()

	type TC struct {
		id OrdUuid7
	}
	type R struct {
		v   driver.Value
		err error
	}

	tbdd.WT(
		TC{id: New()},
		"getting the sql driver value",
		func(t *testing.T, tc TC) R {
			v, err := tc.id.Value()

			return R{v, err}
		},
		"it returns the raw 16 bytes",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.NoError(r.err)
			is.True(driver.IsValue(r.v))

			b, ok := r.v.([]byte)
			is.True(ok)
			is.Equal(tc.id[:], b)
		},
	).Run(t)
}

func TestScan(t *testing.T) {
	t.Parallel()

	src := New()

	type TC struct {
		v any
	}
	type R struct {
		id  OrdUuid7
		err error
	}

	{
		lc := tbdd.WT(
			TC{v: src[:]},
			"scanning a database value of a valid id",
			func(t *testing.T, tc TC) R {
				var id OrdUuid7
				err := id.Scan(tc.v)

				return R{id, err}
			},
			"it loads the id without error",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.NoError(r.err)
				is.Equal(src, r.id)
			},
		)

		lc.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
			return func(yield func(tbdd.TestVariant[TC]) bool) {
				yield(tbdd.TestVariant[TC]{Kind: "string", TC: TC{v: string(src[:])}})
			}
		}

		lc.Run(t)
	}

	{
		lc := tbdd.WT(
			TC{v: int64(1)},
			"scanning an invalid database value into an already-populated id",
			func(t *testing.T, tc TC) R {
				id := src
				err := id.Scan(tc.v)

				return R{id, err}
			},
			"it errors with ErrInvalidScanValue and leaves the destination unchanged",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.ErrorIs(r.err, ErrInvalidScanValue)
				is.Equal(src, r.id)
			},
		)

		lc.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
			return func(yield func(tbdd.TestVariant[TC]) bool) {
				for _, v := range []tbdd.TestVariant[TC]{
					{Kind: "nil", TC: TC{v: nil}},
					{Kind: "short-string", TC: TC{v: string(src[:byteCount-1])}},
					{Kind: "long-string", TC: TC{v: string(src[:]) + "0"}},
					{Kind: "short-bytes", TC: TC{v: src[:byteCount-1]}},
					{Kind: "long-bytes", TC: TC{v: append(bytes.Clone(src[:]), 0)}},
					{Kind: "invalid-marker-string", TC: TC{v: strings.Repeat("\x00", byteCount)}},
					{Kind: "invalid-marker-bytes", TC: TC{v: make([]byte, byteCount)}},
				} {
					if !yield(v) {
						return
					}
				}
			}
		}

		lc.Run(t)
	}
}
