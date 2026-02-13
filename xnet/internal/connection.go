package xnet_i

import (
	"errors"
	"net"
	"syscall"
)

const (
	msgErrRawConnReadFailed    = "syscall.RawConn.Read failed"
	prefixErrRawConnReadFailed = msgErrRawConnReadFailed + ": "
)

var (
	ErrNotSyscallConn  = errors.New("net.Conn instance does not implement syscall.Conn")
	ErrSyscallConnCall = errors.New("SyscallConn call error")

	ErrRawConnReadFailed = errors.New(msgErrRawConnReadFailed)
)

type rawConnReadError struct {
	err error
}

func (e *rawConnReadError) Error() string {
	return prefixErrRawConnReadFailed + e.err.Error()
}

func (e *rawConnReadError) Is(target error) bool {
	return target == ErrRawConnReadFailed || errors.Is(e.err, target)
}

// IsConnected reports whether a TCP connection is still logically open.
// It uses a non-blocking peek to check socket state without consuming data.
//
// This only works reliably on Linux and macOS, where syscall.Conn exposes
// the underlying file descriptor, and syscall.Recvfrom supports MSG_PEEK.
//
// It is possible for the error returned to be nil, but the connection to
// be closed as indicated by a false returned boolean value. It is not possible
// for the boolean to be true and the error to be non-nil.
//
// If you do not want to utilize the error returned, you can call
// IsConnectedNoErr instead.
//
// See https://stackoverflow.com/a/58664631/3200607
//
// A return value of (false, nil) does not indicate that the connection is
// closed (logically or physically) in any way and this function should not
// be used to determine if close needs to be called or already was called.
func IsConnected(conn net.Conn) (bool, error) {

	// supports getting passed a *tls.Conn or similar
	for {
		v, ok := conn.(interface{ NetConn() net.Conn })
		if !ok {
			break
		}
		conn = v.NetConn()
	}

	sconn, ok := conn.(syscall.Conn)
	if !ok {
		return false, ErrNotSyscallConn
	}

	rc, err := sconn.SyscallConn()
	if err != nil {
		return false, errors.Join(ErrSyscallConnCall, err)
	}

	connected := false
	err = rc.Read(func(fd uintptr) bool {
		var buff [1]byte
		n, _, err := syscall.Recvfrom(int(fd), buff[:], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)

		//
		// IGNORE the linting warning about QF1003 here "could use tagged switch"
		//
		// The two values syscall.EWOULDBLOCK and syscall.EAGAIN are NOT guaranteed to always
		// be the same value on all platforms. On some platforms they are the same value,
		// but on others they are different values. Thus we need to check them both explicitly.
		//

		if err == nil {
			if n != 0 {
				// definitely connected, there is data to read
				connected = true
			}
			// ^ else: definitely not connected, equiv to io.EOF
		} else if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			// no-op, definitely connected still, just no data ready to read yet
			connected = true
		}

		return true
	})
	if err != nil {
		return false, &rawConnReadError{err}
	}

	return connected, nil
}

// IsConnectedNoErr behaves like IsConnected, but does not return an error.
// It is useful when you are only interested in the boolean value and not the error.
//
// Note that for the purposes of this function, if any error is encountered while
// checking the connection then false will be returned even if the connection is
// logically still established.
//
// It expects the input to implement syscall.Conn and for that implementation to not
// return an error when calling SyscallConn for the check to be fully reliable.
//
// Therefore this implementation accurately reports if a connection is still
// established, but may return false negatives in some error scenarios. A false
// return value does not guarantee that the connection is no longer established.
//
// Just like IsConnected, a false return value also does not indicate that the
// connections is closed (logically or physically) and should not be used to
// determine if close needs to be called or already was called.
func IsConnectedNoErr(conn net.Conn) bool {

	// supports getting passed a *tls.Conn or similar
	for {
		v, ok := conn.(interface{ NetConn() net.Conn })
		if !ok {
			break
		}
		conn = v.NetConn()
	}

	sconn, ok := conn.(syscall.Conn)
	if !ok {
		return false
	}

	rc, err := sconn.SyscallConn()
	if err != nil {
		return false
	}

	connected := false
	if rc.Read(func(fd uintptr) bool {
		var buff [1]byte
		n, _, err := syscall.Recvfrom(int(fd), buff[:], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)

		//
		// IGNORE the linting warning about QF1003 here "could use tagged switch"
		//
		// The two values syscall.EWOULDBLOCK and syscall.EAGAIN are NOT guaranteed to always
		// be the same value on all platforms. On some platforms they are the same value,
		// but on others they are different values. Thus we need to check them both explicitly.
		//

		if err == nil {
			if n != 0 {
				// definitely connected, there is data to read
				connected = true
			}
			// ^ else: definitely not connected, equiv to io.EOF
		} else if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			// no-op, definitely connected still, just no data ready to read yet
			connected = true
		}

		return true
	}) != nil {
		return false
	}

	return connected
}
