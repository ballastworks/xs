package xnet_i

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func withTestServerConn(b *testing.B, f func(conn net.Conn)) {
	srv := http.Server{
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  30 * time.Second,
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			// no-op handler
		}),
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	shutdownErrChan := make(chan error, 1)
	serveErrChan := make(chan error, 1)
	defer func() {

		if err, ok := <-serveErrChan; ok {
			b.Fatalf("server serve error: %v", err)
		}

		if err, ok := <-shutdownErrChan; ok {
			b.Fatalf("server shutdown error: %v", err)
		}
	}()

	go func() {
		defer close(serveErrChan)

		err := srv.Serve(listener)
		if err != nil && err != http.ErrServerClosed {
			serveErrChan <- err
		}
	}()
	defer func() {
		defer close(shutdownErrChan)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			shutdownErrChan <- err
		}
	}()

	srvAddr := listener.Addr().String()

	conn, err := net.DialTimeout("tcp", srvAddr, 3*time.Second)
	if err != nil {
		b.Fatalf("failed to dial server: %v", err)
	}
	defer conn.Close()

	f(conn)
}

func BenchmarkIsConnected(b *testing.B) {
	withTestServerConn(b, func(conn net.Conn) {

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = IsConnected(conn)
		}

		b.StopTimer()
	})
}

func BenchmarkIsConnectedNoErr(b *testing.B) {
	withTestServerConn(b, func(conn net.Conn) {

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = IsConnectedNoErr(conn)
		}

		b.StopTimer()
	})
}
