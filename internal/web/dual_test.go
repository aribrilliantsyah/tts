package web

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestDualServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tlsConfig, err := GenerateSelfSignedCert(nil, []string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto := "http"
		if r.TLS != nil {
			proto = "https"
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello from " + proto))
	})

	srv := NewDualServer(ln, handler, tlsConfig)
	go func() {
		_ = srv.Serve()
	}()
	defer srv.Close()

	addr := ln.Addr().String()

	// 1. Test Plain HTTP
	respHTTP, err := http.Get("http://" + addr + "/test")
	if err != nil {
		t.Fatalf("http get failed: %v", err)
	}
	bodyHTTP, _ := io.ReadAll(respHTTP.Body)
	respHTTP.Body.Close()
	if string(bodyHTTP) != "hello from http" {
		t.Fatalf("expected 'hello from http', got %q", string(bodyHTTP))
	}

	// 2. Test HTTPS on same port!
	clientHTTPS := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 5 * time.Second,
	}
	respHTTPS, err := clientHTTPS.Get("https://" + addr + "/test")
	if err != nil {
		t.Fatalf("https get failed: %v", err)
	}
	bodyHTTPS, _ := io.ReadAll(respHTTPS.Body)
	respHTTPS.Body.Close()
	if string(bodyHTTPS) != "hello from https" {
		t.Fatalf("expected 'hello from https', got %q", string(bodyHTTPS))
	}
}
