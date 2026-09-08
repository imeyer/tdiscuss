// Package cgocanary exists solely to keep the cgo build path exercised on
// every platform CI tests, including the ones that ship pure-Go binaries.
//
// DO NOT DELETE THIS PACKAGE just because nothing imports it.
//
// tdiscuss builds Linux binaries with pure = "on" (static, for distroless),
// and darwin with pure = "off", because tailscale's certstore needs cgo to
// reach CoreFoundation/Security on macOS (see helper.bzl). Every third-party
// dependency that sets cgo = True - client_golang/prometheus, certstore,
// tailscale/posture - gates its cgo sources to darwin or windows. The net
// effect is that darwin/arm64 is the only configuration that ever invokes the
// Go cgo tool, so a broken cgo toolchain is invisible to the Linux test and
// build jobs and surfaces only on the macOS runner.
//
// That happened: rules_go 0.61.1 could not drive Go 1.27's cgo, which rejected
// the C compiler flags rules_go passed it ("flag provided but not defined:
// -no-canonical-prefixes"). The Go SDK had been bumped to 1.27.1 while
// rules_go stayed put, and it went unnoticed for two months of green Linux
// builds until a darwin release build failed. The same break reproduces on
// Linux the moment anything actually compiles a cgo package.
//
// This package is that something. Its test builds and links C through the
// active cc toolchain on whatever platform CI runs, so a rules_go/Go SDK
// mismatch fails fast on the cheap amd64 runner instead of the macOS one.
package cgocanary

/*
#include <stdlib.h>

static int cgo_canary_probe(int n) { return abs(n) + 1; }
*/
import "C"

// Probe calls into C and back. The value is arbitrary; what matters is that
// compiling, linking, and calling it works at all.
func Probe(n int) int {
	return int(C.cgo_canary_probe(C.int(n)))
}
