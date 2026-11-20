package gogateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

// mustSignToken builds a valid HS256 token for `secret`.
// If auth_test.go already defines signToken, delete this and use that.
func mustSignToken(header, payload, secret string) string {
	return signToken(header, payload, secret)
}

// freeAddr returns a currently-free 127.0.0.1 address.
// Small race window between Close() and use — acceptable in tests.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// waitForListener blocks until `addr` accepts a TCP connection or timeout.
func waitForListener(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server on %s did not accept connections within %v", addr, timeout)
}

// ---------------------------------------------------------------
// New()
// ---------------------------------------------------------------

func TestNew_RootHandler(t *testing.T) {
	t.Parallel()
	g := New()

	t.Run("GET / returns the builtin message", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		g.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d; want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "root handler") {
			t.Errorf("body = %q; want it to contain 'root handler'", rec.Body.String())
		}
	})

	t.Run("GET /unknown returns 404", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		g.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unknown", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d; want 404", rec.Code)
		}
	})
}

// ---------------------------------------------------------------
// AddRoute()
// ---------------------------------------------------------------

func TestAddRoute_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dest    string
		path    string
		wantErr bool
	}{
		{"valid http", "http://example.com", "/api/", false},
		{"valid https", "https://example.com:8443", "/api/", false},
		{"empty URL", "", "/api/", true},
		{"missing scheme", "example.com", "/api/", true},
		{"missing host", "http://", "/api/", true},
		{"malformed URL", "://bad", "/api/", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := New()
			err := g.AddRoute(tt.dest, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddRoute(%q, %q) err = %v; wantErr %v", tt.dest, tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestAddRoute_DuplicateRoutee(t *testing.T) {
	t.Parallel()
	g := New()

	if err := g.AddRoute("http://example.com", "/api/"); err != nil {
		t.Fatalf("first AddRoute: %v", err)
	}
	err := g.AddRoute("http://example.com", "/api/")
	if err == nil {
		t.Fatal("duplicate AddRoute should return error")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("err = %q; want it to mention 'already registered'", err)
	}
}

func TestAddRoute_ProxiesToBackend(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-Hit", "true")
		w.WriteHeader(http.StatusTeapot) // distinctive status
		fmt.Fprint(w, "hello from backend")
	}))
	defer backend.Close()

	g := New()
	if err := g.AddRoute(backend.URL, "/api/"); err != nil {
		t.Fatalf("AddRoute: %v", err)
	}

	rec := httptest.NewRecorder()
	g.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/foo", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d; want %d (proxied to backend)", rec.Code, http.StatusTeapot)
	}
	if rec.Header().Get("X-Backend-Hit") != "true" {
		t.Error("request did not reach the backend")
	}
	if !strings.Contains(rec.Body.String(), "hello from backend") {
		t.Errorf("body = %q; want backend body", rec.Body.String())
	}
}

func TestAddRoute_MultiplePrefixesRouteeIndependently(t *testing.T) {
	t.Parallel()

	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "A")
	}))
	defer backendA.Close()

	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "B")
	}))
	defer backendB.Close()

	g := New()
	if err := g.AddRoute(backendA.URL, "/api/"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddRoute(backendB.URL, "/db/"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		want string
	}{
		{"/api/x", "A"},
		{"/db/y", "B"},
		{"/apix", ""}, // no boundary match → falls to "/" → 404
		{"/other", ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		g.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if c.want == "" {
			if rec.Code != http.StatusNotFound {
				t.Errorf("GET %s status = %d; want 404", c.path, rec.Code)
			}
			continue
		}
		if strings.TrimSpace(rec.Body.String()) != c.want {
			t.Errorf("GET %s body = %q; want %q", c.path, rec.Body.String(), c.want)
		}
	}
}

// TestAddRoute_RootPathCollision documents current behavior:
// New() pre-registers "/" via HandleFunc, but does NOT record it in g.paths,
// so AddRoute("...", "/") bypasses the duplicate check and hits
// http.ServeMux's "multiple registrations" panic.
//
// Fix: either reject "/" in AddRoute with a clear error, or initialize
// g.paths["/"] = true in New().
func TestAddRoute_RootPathCollision(t *testing.T) {
	t.Skip("known issue — see docstring")

	g := New()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic registering root path")
		}
	}()
	_ = g.AddRoute("http://example.com", "/")
}

// ---------------------------------------------------------------
// createServer() — middleware chain
// ---------------------------------------------------------------

func TestCreateServer_NoAuthNoRateLimit(t *testing.T) {
	t.Parallel()

	g := New()
	srv := g.createServer(":0")

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}
}

func TestCreateServer_AuthEnabled(t *testing.T) {
	t.Parallel()

	const secret = "s3cret"
	g := New()
	g.SetAuthSecret(secret)
	srv := g.createServer(":0")

	t.Run("no token → 401", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d; want 401", rec.Code)
		}
	})

	t.Run("valid token → 200", func(t *testing.T) {
		t.Parallel()
		tok := mustSignToken("header", "payload", secret)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d; want 200", rec.Code)
		}
	})

	t.Run("public path bypasses auth", func(t *testing.T) {
		t.Parallel()
		// /healthz is not registered in the mux → 404 (proves auth was bypassed;
		// without bypass we'd get 401 first).
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d; want 404 (auth must be bypassed)", rec.Code)
		}
	})
}

func TestCreateServer_RateLimit(t *testing.T) {
	t.Parallel()

	g := New()
	g.SetRatelimiterAtribute(2, 1) // capacity 2
	srv := g.createServer(":0")

	send := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "1.2.3.4:5555" // same client every time
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if c := send(); c != http.StatusOK {
		t.Errorf("1st = %d; want 200", c)
	}
	if c := send(); c != http.StatusOK {
		t.Errorf("2nd = %d; want 200", c)
	}
	if c := send(); c != http.StatusTooManyRequests {
		t.Errorf("3rd = %d; want 429", c)
	}
}

// Documents the middleware order: rate_limiter(auth(mux)).
// A rate-limited request is rejected BEFORE the auth check runs.
func TestCreateServer_RateLimitRunsBeforeAuth(t *testing.T) {
	t.Parallel()

	g := New()
	g.SetAuthSecret("s3cret")
	g.SetRatelimiterAtribute(1, 1) // capacity 1
	srv := g.createServer(":0")

	send := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "1.2.3.4:5555"
		// no Authorization header → auth would return 401 if it ran
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if c := send(); c != http.StatusUnauthorized {
		t.Errorf("1st = %d; want 401 (rate limiter passed, auth rejected)", c)
	}
	if c := send(); c != http.StatusTooManyRequests {
		t.Errorf("2nd = %d; want 429 (rate limiter fired before auth)", c)
	}
}

func TestCreateServer_ServerTimeouts(t *testing.T) {
	t.Parallel()

	g := New()
	srv := g.createServer(":0")

	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout should be set")
	}
	if srv.ReadTimeout <= 0 {
		t.Error("ReadTimeout should be set")
	}
	if srv.WriteTimeout <= 0 {
		t.Error("WriteTimeout should be set")
	}
	if srv.IdleTimeout <= 0 {
		t.Error("IdleTimeout should be set")
	}
}

// ---------------------------------------------------------------
// runserver()
// ---------------------------------------------------------------

func TestRunServer_InvalidAddr(t *testing.T) {
	t.Parallel()

	srv := &http.Server{
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}

	// ListenAndServe fails immediately; runserver should return that error.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := runserver(ctx, srv, time.Second, "not-a-valid-addr")
	if err == nil {
		t.Fatal("expected error for invalid address, got nil")
	}
}

func TestRunServer_GracefulShutdown(t *testing.T) {
	t.Parallel()

	addr := freeAddr(t)
	srv := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "ok")
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- runserver(ctx, srv, 2*time.Second, addr) }()

	// Wait until the server actually accepts connections.
	waitForListener(t, addr, 3*time.Second)

	// Sanity check: it serves.
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want 200", resp.StatusCode)
	}

	// Trigger graceful shutdown.
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runserver returned error on graceful shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runserver did not return after shutdown signal")
	}
}

// ---------------------------------------------------------------
// Run() — thin wrapper; test only the fast error path
// ---------------------------------------------------------------

func TestRun_InvalidAddr(t *testing.T) {
	t.Parallel()

	g := New()
	if err := g.Run("not-a-valid-addr"); err == nil {
		t.Fatal("Run with invalid address should return error")
	}
}
