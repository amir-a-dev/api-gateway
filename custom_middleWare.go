package gogateway

import (
	"errors"
	"net/http"
)

// middle wares who run befor HS256Verifier(jwt) and	RatelimiterMW(rate limmier) wrap the Handler
func (g *Gateway) Use(middle_wares ...func(http.Handler) http.Handler) error {

	// check for nil functions
	for _, mw := range middle_wares {
		if mw == nil {
			return errors.New("gateway: nil middle wares")
		}
	}

	g.outerMws = append(g.outerMws, middle_wares...)
	return nil
}

// middle wares who run after HS256Verifier(jwt) and RatelimiterMW(rate limmier) wrap the Handler
func (g *Gateway) UseInner(middle_wares ...func(http.Handler) http.Handler) error {

	// check for nil functions
	for _, mw := range middle_wares {
		if mw == nil {
			return errors.New("gateway: nil middle wares")
		}
	}

	g.innerMws = append(g.innerMws, middle_wares...)
	return nil
}
