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
	"slices"
	"syscall"
	"time"
)

type Gateway struct {
	mux          *http.ServeMux
	paths        map[string]bool
	secret       string
	rlCapacity   float64
	rlRefillRate float64
	outerMws     []func(http.Handler) http.Handler
	innerMws     []func(http.Handler) http.Handler
}

// initiate the gate way
func New() *Gateway {
	g := &Gateway{
		mux:   http.NewServeMux(),
		paths: make(map[string]bool),
	}
	//a route for testing the gate way is running or not
	g.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintln(w, "this is root handler from Gateway")
	})
	return g
}

// add new route
func (g *Gateway) AddRoute(destinationURL, matchPath string) error {
	// check for duplicate routes
	if g.paths[matchPath] {
		return fmt.Errorf("route %q already registered", matchPath)
	}

	target, err := url.Parse(destinationURL)
	// Check URL validity
	if err != nil {
		return fmt.Errorf("invalid destination URL %q: %w", destinationURL, err)
	}
	if target.Scheme == "" || target.Host == "" {
		return fmt.Errorf("destination URL %q must include scheme and host", destinationURL)
	}
	//the reverse proxy for client route
	proxy := httputil.NewSingleHostReverseProxy(target)
	//add a Handle for as reverse proxy for Handler(mux)
	g.mux.Handle(matchPath, proxy)
	// set this route as true in paths table(to prevent duplicate routes)
	g.paths[matchPath] = true

	return nil
}

// set authentication for gate way via set the secret key(check the createServer function)
func (g *Gateway) SetAuthSecret(secret string) {
	g.secret = secret
}

// set rate limiter for gate way via set the capacity and refill Rate(check the createServer function)
func (g *Gateway) SetRatelimiterAtribute(capacity, refillRate float64) {
	g.rlCapacity = capacity
	g.rlRefillRate = refillRate
}

// configure the http server
func (g *Gateway) createServer(addr string) *http.Server {
	// this let us wrap middleware for out Handler
	var handler http.Handler = g.mux
	// the handlers that must be called after auth and rate limit(inner middleware)
	for _, v := range slices.Backward(g.innerMws) {
		handler = v(handler)
	}
	// for adding authentication(built in) middleware
	if g.secret != "" {
		publicPaths := map[string]bool{
			"/login":   true,
			"/healthz": true, // 👈 keep the name consistent
		}
		// the middleware
		handler = HS256Verifier(g.secret, publicPaths, handler)
	}
	// for adding rate limiter  middleware
	if g.rlCapacity > 0 && g.rlRefillRate > 0 {
		//initiate middleware
		rate_limiter := NewRateLimiter(g.rlCapacity, g.rlRefillRate)
		//warp the middleware
		handler = rate_limiter.RatelimiterMW(handler)
	}

	// the handlers that must be called before auth and rate limit(outer middleware)
	for _, v := range slices.Backward(g.outerMws) {
		handler = v(handler)
	}
	// initial configuration for http server(the http that client gate way listening on)
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
}

// run the server with gracefully shutdown
func runserver(ctx context.Context, server *http.Server, shutdownTime time.Duration, addr string) error {

	// notify the Interrupt(shutdown) signal
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)

	go func() {
		// run the server that Gateway listening on
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

	// create a context for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTime)
	defer cancel()

	// Shutdown server
	if err := server.Shutdown(shutdownCtx); err != nil {
		if closeErr := server.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	//return the value of serverErr(if server get error in gracefully shutdown)
	if err, ok := <-serverErr; ok && err != nil {
		log.Printf("server error during shutdown: %v", err)
	}

	log.Println("server exited gracefully")
	return nil

}

// run gateway with all configure client set(route, middleware , auth ,rate limiter)
// the final function that must be invoke
func (g *Gateway) Run(addr string) error {

	return runserver(context.Background(), g.createServer(addr), 3*time.Second, addr)

}
