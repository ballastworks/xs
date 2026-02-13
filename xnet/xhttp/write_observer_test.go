package xhttp

import (
	"bufio"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func structMethodSigs(t reflect.Type) []string {
	numMethods := t.NumMethod()
	for i := range t.NumMethod() {
		if !t.Method(i).IsExported() {
			numMethods--
		}
	}

	if numMethods == 0 {
		return nil
	}
	result := make([]string, 0, numMethods)

	for i := range t.NumMethod() {
		m := t.Method(i)
		if !m.IsExported() {
			continue
		}

		sig := m.Type.String()
		i := strings.IndexAny(sig, ",)")
		if i <= 0 {
			panic("unable to parse struct method signature")
		}

		if sig[i] == ',' {
			i++
		}

		result = append(result, m.Name+"("+strings.TrimSpace(sig[i:]))
	}

	slices.Sort(result)

	return result
}

func interfaceMethodSigs(t reflect.Type) []string {
	numMethods := t.NumMethod()
	for i := range t.NumMethod() {
		if !t.Method(i).IsExported() {
			numMethods--
		}
	}

	if numMethods == 0 {
		return nil
	}
	result := make([]string, 0, numMethods)

	for i := range t.NumMethod() {
		m := t.Method(i)
		if !m.IsExported() {
			continue
		}

		sig := m.Type.String()
		if !strings.HasPrefix(sig, "func(") {
			panic("unable to parse interface method signature")
		}

		result = append(result, m.Name+sig[4:])
	}

	slices.Sort(result)

	return result
}

func methodSigs(v any) []string {
	if v == nil {
		panic("nil value passed to methodSigs")
	}

	t := reflect.TypeOf(v)

	switch t.Kind() {
	case reflect.Pointer:
		if vt := t.Elem(); vt.Kind() == reflect.Interface {
			return interfaceMethodSigs(vt)
		} else if vt.Kind() == reflect.Struct {
		} else {
			panic("expected a pointer to struct or interface type")
		}
	case reflect.Interface:
		return interfaceMethodSigs(t)
	case reflect.Struct:
	default:
		panic("expected struct or interface; or pointer to one of the previous types")
	}

	return structMethodSigs(t)
}

func TestWriteObserverExposure(t *testing.T) {
	// writeObserverState is composed of a http.ResponseWriter interface and a *http.ResponseController instance
	//
	// writeObserver is composed of a writeObserverState as well
	//
	// The goal of this function is to ensure that only these interfaces are implemented and exposed. Should any
	// if the compositional concrete types gain additional public behavior, this test will fail in some fashion.
	//
	// Additional public behavior must be both understood tracked accordingly as the observer intends to wrap
	// such details and listen for mutation events.

	type expResponseWriter interface {
		Header() http.Header
		Write([]byte) (int, error)
		WriteHeader(statusCode int)
	}
	type expResponseController interface {
		EnableFullDuplex() error
		Flush() error
		Hijack() (net.Conn, *bufio.ReadWriter, error)
		SetReadDeadline(deadline time.Time) error
		SetWriteDeadline(deadline time.Time) error
	}

	// verify http.ResponseWriter exactly matches expResponseWriter
	{
		var _ = http.ResponseWriter((expResponseWriter)(nil))
		var _ = expResponseWriter((http.ResponseWriter)(nil))
	}

	// verify *http.ResponseController exactly matches expResponseController
	{
		var _ = expResponseController((*http.ResponseController)(nil))

		// list all exposed methods and ensure that it is within the expResponseController interface

		exp := methodSigs((*expResponseController)(nil))
		act := methodSigs(&http.ResponseController{})

		if !slices.Equal(exp, act) {
			t.Fatal("*http.ResponseController public methods have changed and there is likely a new method to support")
		}
	}

	// verify *writeObserver public interface surface remains well controlled.
	{
		type expSurfaceOfWriteObserver interface {
			HTTPWriteStatus() writeObserverStatusResp
		}

		exp := methodSigs((*interface {
			expSurfaceOfWriteObserver
			expResponseController
			expResponseWriter
		})(nil))
		act := methodSigs(&writeObserver{})

		if !slices.Equal(exp, act) {
			t.Fatal("*writeObserver public methods have changed and it is likely a public method was added by accident")
		}
	}
}
