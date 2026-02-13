package xnet

import (
	"iter"
	"net/http"
	"testing"

	"github.com/josephcopenhaver/tbdd-go"
	"github.com/stretchr/testify/assert"
)

// httpTransportDTO is a stable interface checkpoint for getting and setting the underlying protocol response on a transport response.
type httpTransportDTO interface {
	SetProtocolResponse(*http.Response)
	ProtocolResponse() *http.Response
}

func TestTransportResponse(t *testing.T) {
	t.Parallel()

	type TC struct {
		v any
	}

	type R struct {
		v  httpTransportDTO
		ok bool
	}

	tc := tbdd.GWT(
		// given
		TC{
			v: &Response[http.Response]{},
		},
		"type implements httpTransportDTO",
		nil,
		// when
		"checking interface",
		func(t *testing.T, tc TC) R {
			is := assert.New(t)

			is.NotNil(tc.v)

			v, ok := tc.v.(httpTransportDTO)
			return R{v, ok}
		},
		// then
		"it has working getter and setter",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.NotNil(r.v)
			is.True(r.ok)

			is.Nil(r.v.ProtocolResponse())

			resp := &http.Response{}
			r.v.SetProtocolResponse(resp)

			is.NotNil(r.v.ProtocolResponse())
			is.Equal(resp, r.v.ProtocolResponse())
		},
	)

	tc.Variants = func(_ *testing.T, tc TC) iter.Seq[tbdd.TestVariant[TC]] {
		return func(yield func(tbdd.TestVariant[TC]) bool) {
			{
				yield(tbdd.TestVariant[TC]{
					Kind: "struct-composition",
					TC: TC{
						v: &struct {
							Response[http.Response]
						}{},
					},
				})
			}
		}
	}

	tc.Run(t)
}
