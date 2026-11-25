# gogateway

A small API gateway written in Go with **zero third-party dependencies**.

Routes HTTP requests to multiple backend services with JWT authentication, rate
limiting, and composable middleware — using only the standard library.

## What it is

A **library** you import and configure in your own Go program. It handles:

- Prefix-based reverse proxy routing
- HS256 JWT authentication
- In-memory token-bucket rate limiting
- Custom middleware (before or after auth)

## What it is not

- Not a Kubernetes ingress controller
- Not a replacement for nginx, Envoy, or Traefik
- Not a distributed system — the rate limiter is per-process

It's a small, readable, single-binary gateway. Good for side projects, homelabs,
internal tools, and learning how gateways work.

## Features

| Feature                                  | Status          |
| ---------------------------------------- | --------------- |
| Prefix routing to multiple backends      | ✅              |
| HS256 JWT verification                   | ✅              |
| Public paths (login, health) bypass auth | ✅              |
| Token-bucket rate limiting (per IP)      | ✅              |
| Custom middleware — outer (before auth)  | ✅              |
| Custom middleware — inner (after auth)   | ✅              |
| Graceful shutdown                        | ✅              |
| Zero third-party dependencies            | ✅              |
| Multi-instance rate limiting (Redis)     | ❌ out of scope |
| RS256 / JWKS                             | ❌ out of scope |
| WebSocket proxying                       | ❌ untested     |

## Quick start

```go
package main

import (
    "log"
    "gogateway"
)

func main() {
    g := gogateway.New()

    // Route /api/users/* and /api/orders/* to two different backends
    if err := g.AddRoute("http://localhost:9001", "/api/users/"); err != nil {
        log.Fatal(err)
    }
    if err := g.AddRoute("http://localhost:9002", "/api/orders/"); err != nil {
        log.Fatal(err)
    }

    // Require HS256 JWT on all routes except /login and /healthz
    g.SetAuthSecret("your-secret-key")

    // Allow 10 requests burst, refill 5 per second per client IP
    g.SetRatelimiterAtribute(10, 5)

    // Add custom middleware
    if err := g.Use(myLoggingMiddleware); err != nil {
        log.Fatal(err)
    }

    if err := g.Run(":9090"); err != nil {
        log.Fatal(err)
    }
}
```
