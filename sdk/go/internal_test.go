package scruff

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// In-package (not scruff_test) because it drives the fixture's dedicated
// "reap-refused" verb directly through run() — no exported method has an
// argv shape that lands on that first arg.
func TestErrorMapping_NonZeroExitCarriesTheRealCode(t *testing.T) {
	c := &Client{Bin: "./testdata/fake-scruff.sh"}
	_, _, err := c.run(context.Background(), "reap-refused")
	if err == nil {
		t.Fatal("run(reap-refused): want error, got nil")
	}

	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v (%T), want *Error", err, err)
	}
	if e.Code != ExitRefused {
		t.Errorf("Code = %d, want %d", e.Code, ExitRefused)
	}
	if !e.Refused() {
		t.Error("Refused() = false, want true")
	}
	if e.Degraded() {
		t.Error("Degraded() = true, want false")
	}
	if !strings.Contains(e.Stderr, "occupied") {
		t.Errorf("Stderr = %q, want it to contain %q", e.Stderr, "occupied")
	}
}
