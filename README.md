# tdiscuss
Discussion board for your tailnet

## Why?

Why not? Who doesn't want a discussion board for their tailnet? Honestly, I've often found myself wanting a small/simple/easy-to-deploy discussion board [a la pgBoard](https://github.com/pgBoard/pgBoard). Tailscale launching golink and tclip, which both use the tailscale go library to expose the application securely, with user auth built-in!.

## Running

Coming soon...

## Building

1. Install [Bazelisk](https://github.com/bazelbuild/bazelisk#installation).
1. `bazel build //cmd/tdiscuss:tdiscuss`
1. Find the resulting binary in `$(bazelisk info bazel-bin)/cmd/tdiscuss/tdiscuss_/tdiscuss`
1. Hope that it doesn't panic with `panic: godebug: Value of name not listed in godebugs.All:` as the error
