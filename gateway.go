package gogateway

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type Gateway struct {
	mux          *http.ServeMux
	paths        map[string]bool
	secret       string
	rlCapacity   float64
	rlRefillRate float64
}

func New() *Gateway {
	g := &Gateway{
		mux:   http.NewServeMux(),
		paths: make(map[string]bool),
	}
	g.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintln(w, "this is root handler from Gateway")
	})
	return g
}

func (g *Gateway) AddServer(destinationURL, matchPath string) error {
	if g.paths[matchPath] {
		return fmt.Errorf("route %q already registered", matchPath)
	}

	target, err := url.Parse(destinationURL)
	if err != nil {
		return fmt.Errorf("invalid destination URL %q: %w", destinationURL, err)
	}
	if target.Scheme == "" || target.Host == "" {
		return fmt.Errorf("destination URL %q must include scheme and host", destinationURL)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	g.mux.Handle(matchPath, proxy)

	g.paths[matchPath] = true

	return nil
}

func (g *Gateway) SetAuthSecret(secret string) {
	g.secret = secret
}

func (g *Gateway) SetRatelimiterAtribute(capacity, refillRate float64) {
	g.rlCapacity = capacity
	g.rlRefillRate = refillRate
}

func (g *Gateway) createServer(addr string) *http.Server {

	var handler http.Handler = g.mux
	if g.secret != "" {
		publicPaths := map[string]bool{
			"/login":   true,
			"/healthz": true, // 👈 keep the name consistent
		}
		handler = HS256Verifier(g.secret, publicPaths, handler)
	}

	if g.rlCapacity > 0 && g.rlRefillRate > 0 {
		rate_limiter := NewRateLimiter(g.rlCapacity, g.rlRefillRate)

		handler = rate_limiter.RatelimiterMW(handler)
	}

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
}

func runserver(ctx context.Context, server *http.Server, shutdownTime time.Duration, addr string) error {

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)

	go func() {
		log.Printf("gateway listening on %s", addr)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			return err
		}
		// channel closed without error → server exited unexpectedly? just return
		return nil
	case <-ctx.Done():
		log.Println("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTime)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		if closeErr := server.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	if err, ok := <-serverErr; ok && err != nil {
		log.Printf("server error during shutdown: %v", err)
	}

	log.Println("server exited gracefully")
	return nil

}

func (g *Gateway) Run(addr string) error {

	return runserver(context.Background(), g.createServer(addr), 3*time.Second, addr)

}
