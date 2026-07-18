"""Define platform, arch pairs to build"""

# NOTE: darwin binaries are built with cgo (see helper.bzl) because tsnet pulls
# github.com/tailscale/certstore, whose macOS implementation links the
# CoreFoundation/Security frameworks. They build only on a macOS host (the CI
# Build matrix routes the darwin leg to a macos runner) and cannot be
# cross-compiled from Linux. Linux binaries remain pure/static.
#
# Only darwin/arm64 (Apple Silicon) is built: Apple discontinued x86_64 and
# GitHub is retiring Intel macOS runners (macos-13 already gone; all Intel
# runners end ~Fall 2027). Add ("darwin", "amd64") back with a macos-15-intel
# runner if Intel coverage is ever needed before then.
GO_PLATFORMS = [
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("darwin", "arm64"),
]

# Mapping from Go OS names to @platforms//os: names
GO_OS_TO_PLATFORM = {
    "darwin": "macos",
    "linux": "linux",
}

# Mapping from Go architecture names to @platforms//cpu: names
GO_ARCH_TO_PLATFORM = {
    "amd64": "x86_64",
    "arm64": "arm64",
}
