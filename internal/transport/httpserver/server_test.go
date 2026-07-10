package httpserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServerGracefulShutdown(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	server := New(handler, time.Second, time.Second, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()

	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(time.Second)
	for {
		resp, requestErr := client.Get("http://" + listener.Addr().String())
		if requestErr == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop gracefully")
	}
}
