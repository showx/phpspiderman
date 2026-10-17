package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryThenOK(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			http.Error(w, "no", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c, err := New(2*time.Second, 1, ProxyConfig{}, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(context.Background(), http.MethodGet, srv.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "ok" || n.Load() != 2 {
		t.Fatalf("resp=%s n=%d", resp.Body, n.Load())
	}
}

func TestNoRetryOn404(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c, err := New(2*time.Second, 3, ProxyConfig{}, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(context.Background(), http.MethodGet, srv.URL, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if n.Load() != 1 {
		t.Fatalf("retries=%d", n.Load())
	}
}

func TestBadProxy(t *testing.T) {
	if _, err := New(time.Second, 0, ProxyConfig{URL: "ftp://127.0.0.1:1"}, nil, 1); err == nil {
		t.Fatal("expected proxy error")
	}
}
