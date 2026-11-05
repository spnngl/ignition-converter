package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestServeShutdown checks that serve, its context ended, answers the review in
// flight before it returns, and returns nil.
func TestServeShutdown(t *testing.T) {
	// httptest's certificate for 127.0.0.1, and a client trusting it.
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS()
	client := ts.Client()
	tlsConfig := ts.TLS.Clone()
	ts.Close()

	started, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			_, _ = io.WriteString(w, "ok")
		}),
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: time.Second,
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln) }()

	answered := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+ln.Addr().String(), nil)
		if err != nil {
			answered <- err
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			answered <- err
			return
		}
		answered <- resp.Body.Close()
	}()

	<-started
	cancel()
	select {
	case err := <-served:
		t.Fatalf("serve returned with a review in flight: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-answered; err != nil {
		t.Fatalf("review in flight: %v", err)
	}
	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
}
