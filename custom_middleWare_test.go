package gogateway

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// tagMiddleware returns a middleware that appends its name to a response
// header, so tests can verify which middleware ran and in what order.
func tagMiddleware(name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("X-MW", name)
			next.ServeHTTP(w, r)
		})
	}
}

// passthrough is a plain no-op middleware.
func passthrough(next http.Handler) http.Handler { return next }

// runChain applies the middlewares to a terminal handler in standard
// "outermost = first added" order and returns the response recorder.
func runChain(mws []func(http.Handler) http.Handler) *httptest.ResponseRecorder {
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	for _, v := range slices.Backward(mws) {
		h = v(h)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec
}

// -------------------- Use --------------------

func TestUse_AppendsSingle(t *testing.T) {
	g := New()
	if len(g.outerMws) != 0 {
		t.Fatalf("expected empty outerMws, got %d", len(g.outerMws))
	}

	if err := g.Use(passthrough); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.outerMws) != 1 {
		t.Fatalf("expected 1 outerMw, got %d", len(g.outerMws))
	}
}

func TestUse_AppendsMultipleInOneCall(t *testing.T) {
	g := New()
	if err := g.Use(passthrough, passthrough, passthrough); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.outerMws) != 3 {
		t.Fatalf("expected 3, got %d", len(g.outerMws))
	}
}

func TestUse_AccumulatesAcrossCalls(t *testing.T) {
	g := New()
	_ = g.Use(passthrough)
	_ = g.Use(passthrough)
	_ = g.Use(passthrough)
	if len(g.outerMws) != 3 {
		t.Fatalf("expected 3, got %d", len(g.outerMws))
	}
}

func TestUse_RejectsNil(t *testing.T) {
	g := New()
	err := g.Use(nil)
	if err == nil {
		t.Fatal("expected error for nil middleware")
	}
	if len(g.outerMws) != 0 {
		t.Fatalf("nil must not be appended, got %d", len(g.outerMws))
	}
}

func TestUse_RejectsNilInMiddle_Atomically(t *testing.T) {
	g := New()
	err := g.Use(passthrough, nil, passthrough)
	if err == nil {
		t.Fatal("expected error")
	}
	// Critical: even the valid middlewares must NOT have been appended,
	// otherwise a partial chain gets installed and the caller can't undo it.
	if len(g.outerMws) != 0 {
		t.Fatalf("expected atomic rejection (0 appended), got %d", len(g.outerMws))
	}
}

func TestUse_PreservesOrder(t *testing.T) {
	g := New()
	_ = g.Use(tagMiddleware("a"), tagMiddleware("b"))
	_ = g.Use(tagMiddleware("c"))

	rec := runChain(g.outerMws)
	got := rec.Header().Values("X-MW")
	want := []string{"a", "b", "c"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch at %d: got %v, want %v", i, got, want)
		}
	}
}

// -------------------- UseInner --------------------

func TestUseInner_AppendsSingle(t *testing.T) {
	g := New()
	if err := g.UseInner(passthrough); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.innerMws) != 1 {
		t.Fatalf("expected 1 innerMw, got %d", len(g.innerMws))
	}
}

func TestUseInner_RejectsNil(t *testing.T) {
	g := New()
	if err := g.UseInner(nil); err == nil {
		t.Fatal("expected error for nil middleware")
	}
	if len(g.innerMws) != 0 {
		t.Fatalf("nil must not be appended, got %d", len(g.innerMws))
	}
}

func TestUseInner_RejectsNilInMiddle_Atomically(t *testing.T) {
	g := New()
	if err := g.UseInner(passthrough, nil, passthrough); err == nil {
		t.Fatal("expected error")
	}
	if len(g.innerMws) != 0 {
		t.Fatalf("expected atomic rejection, got %d", len(g.innerMws))
	}
}

func TestUseInner_PreservesOrder(t *testing.T) {
	g := New()
	_ = g.UseInner(tagMiddleware("x"), tagMiddleware("y"))

	rec := runChain(g.innerMws)
	got := rec.Header().Values("X-MW")
	want := []string{"x", "y"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch at %d: got %v, want %v", i, got, want)
		}
	}
}

// -------------------- Interaction --------------------

func TestUseAndUseInner_AreIndependent(t *testing.T) {
	g := New()

	_ = g.Use(tagMiddleware("outer"))
	_ = g.UseInner(tagMiddleware("inner"))

	if len(g.outerMws) != 1 {
		t.Fatalf("outerMws: expected 1, got %d", len(g.outerMws))
	}
	if len(g.innerMws) != 1 {
		t.Fatalf("innerMws: expected 1, got %d", len(g.innerMws))
	}
}

func TestUseAndUseInner_NilDoesNotTouchOther(t *testing.T) {
	g := New()

	_ = g.Use(tagMiddleware("outer"))

	if err := g.UseInner(nil); err == nil {
		t.Fatal("expected error")
	}
	// A failed UseInner must not disturb the already-registered outer chain.
	if len(g.outerMws) != 1 {
		t.Fatalf("outerMws corrupted by failed UseInner: got %d", len(g.outerMws))
	}
	if len(g.innerMws) != 0 {
		t.Fatalf("innerMws should be empty, got %d", len(g.innerMws))
	}
}
