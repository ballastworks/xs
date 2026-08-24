//go:build go1.27

package orduuid7

import (
	"bytes"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"iter"
	"strings"
	"testing"

	"github.com/josephcopenhaver/tbdd-go"
	"github.com/stretchr/testify/assert"
)

// verify that the type implements the json v2 interfaces
var (
	_ jsonv2.MarshalerTo     = OrdUuid7{}
	_ jsonv2.UnmarshalerFrom = &OrdUuid7{}
)

var errWriteFailed = errors.New("write failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errWriteFailed
}

func TestMarshalJSONTo(t *testing.T) {
	t.Parallel()

	type TC struct {
		id OrdUuid7
	}

	{
		type R struct {
			direct    []byte
			directErr error
			viaV2     []byte
			viaV2Err  error
		}

		tbdd.WT(
			TC{id: New()},
			"marshaling a valid id to a jsontext encoder directly and through encoding/json/v2",
			func(t *testing.T, tc TC) R {
				var buf bytes.Buffer
				directErr := tc.id.MarshalJSONTo(jsontext.NewEncoder(&buf))

				viaV2, viaV2Err := jsonv2.Marshal(tc.id)

				return R{buf.Bytes(), directErr, viaV2, viaV2Err}
			},
			"both produce the double-quoted hex string",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				exp := `"` + tc.id.String() + `"`

				is.NoError(r.directErr)
				is.Equal(exp, strings.TrimSpace(string(r.direct)))

				is.NoError(r.viaV2Err)
				is.Equal(exp, string(r.viaV2))
			},
		).Run(t)
	}

	{
		type R struct {
			out []byte
			err error
		}

		tbdd.WT(
			TC{},
			"marshaling an invalid id to a jsontext encoder",
			func(t *testing.T, tc TC) R {
				var buf bytes.Buffer
				err := tc.id.MarshalJSONTo(jsontext.NewEncoder(&buf))

				return R{buf.Bytes(), err}
			},
			"it errors with ErrCannotMarshalValue and writes nothing",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.ErrorIs(r.err, ErrCannotMarshalValue)
				is.Empty(r.out)
			},
		).Run(t)

		tbdd.WT(
			TC{id: New()},
			"marshaling a valid id to an encoder whose writer always fails",
			func(t *testing.T, tc TC) R {
				err := tc.id.MarshalJSONTo(jsontext.NewEncoder(failingWriter{}))

				return R{nil, err}
			},
			"it returns the write error",
			func(t *testing.T, tc TC, r R) {
				assert.New(t).ErrorIs(r.err, errWriteFailed)
			},
		).Run(t)
	}
}

func TestUnmarshalJSONFrom(t *testing.T) {
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
			"unmarshaling a compact quoted hex string from a jsontext decoder",
			func(t *testing.T, tc TC) R {
				var id OrdUuid7
				err := id.UnmarshalJSONFrom(jsontext.NewDecoder(strings.NewReader(tc.text)))

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
			TC{text: `"` + src.String() + `"`},
			"unmarshaling via encoding/json/v2",
			func(t *testing.T, tc TC) R {
				var id OrdUuid7
				err := jsonv2.Unmarshal([]byte(tc.text), &id)

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
			TC{text: `"` + jsonEscapeHex("00000000000000000000000000000027") + `"`},
			"unmarshaling an escape-inflated JSON string from a jsontext decoder",
			func(t *testing.T, tc TC) R {
				id := src
				err := id.UnmarshalJSONFrom(jsontext.NewDecoder(strings.NewReader(tc.text)))

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
			text             string
			allowInvalidUTF8 bool
			expErrIs         error
		}
		type R struct {
			id  OrdUuid7
			err error
		}

		lc := tbdd.WT(
			TC{text: "null", expErrIs: ErrCannotUnmarshalNullJsonValue},
			"unmarshaling an invalid document from a jsontext decoder into an already-populated id",
			func(t *testing.T, tc TC) R {
				var opts []jsontext.Options
				if tc.allowInvalidUTF8 {
					opts = append(opts, jsontext.AllowInvalidUTF8(true))
				}

				id := src
				err := id.UnmarshalJSONFrom(jsontext.NewDecoder(strings.NewReader(tc.text), opts...))

				return R{id, err}
			},
			"it errors and leaves the destination unchanged",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				if tc.expErrIs != nil {
					is.ErrorIs(r.err, tc.expErrIs)
				} else {
					is.Error(r.err)
				}
				is.Equal(src, r.id)
			},
		)

		lc.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
			return func(yield func(tbdd.TestVariant[TC]) bool) {
				for _, v := range []tbdd.TestVariant[TC]{
					{Kind: "empty-input", TC: TC{text: "", expErrIs: io.EOF}},
					{Kind: "garbage-input", TC: TC{text: "!"}},
					{Kind: "non-string-kind-number", TC: TC{text: "123", expErrIs: ErrInvalidJsonUnmarshalText}},
					{Kind: "non-string-kind-object", TC: TC{text: "{}", expErrIs: ErrInvalidJsonUnmarshalText}},
					{Kind: "truncated-string", TC: TC{text: `"` + src.String()}},
					{Kind: "too-short", TC: TC{text: `"` + src.String()[:marshaledByteCount-1] + `"`, expErrIs: ErrInvalidJsonUnmarshalText}},
					{Kind: "too-long", TC: TC{text: `"` + strings.Repeat("0", marshaledByteCount*6+1) + `"`, expErrIs: ErrInvalidJsonUnmarshalText}},
					{Kind: "compact-invalid-hex", TC: TC{text: `"` + strings.Repeat("zz", byteCount) + `"`, expErrIs: ErrInvalidUnmarshalText}},
					{Kind: "compact-valid-hex-invalid-marker", TC: TC{text: `"` + strings.Repeat("00", byteCount) + `"`, expErrIs: ErrInvalidUnmarshalText}},
					{Kind: "escaped-invalid-hex", TC: TC{text: `"` + jsonEscapeHex("z") + strings.Repeat("z", marshaledByteCount-1) + `"`, expErrIs: ErrInvalidUnmarshalText}},
					{Kind: "escaped-wrong-decoded-length", TC: TC{text: `"` + jsonEscapeHex("000000") + `"`, expErrIs: ErrInvalidUnmarshalText}},
					{Kind: "invalid-utf8-in-string", TC: TC{text: `"` + strings.Repeat("a", marshaledByteCount) + "\xff" + `"`, allowInvalidUTF8: true}},
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
