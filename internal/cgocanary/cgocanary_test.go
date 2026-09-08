package cgocanary

import "testing"

// TestProbe is a build canary. If cgo is broken in the toolchain, this package
// fails to compile and this test never runs - which is the whole point. The
// assertion just confirms the C call actually round-tripped.
func TestProbe(t *testing.T) {
	if got, want := Probe(-41), 42; got != want {
		t.Errorf("Probe(-41) = %d, want %d", got, want)
	}
}
