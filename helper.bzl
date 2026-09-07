"""Helper module to create binaries per PLATFORM-ARCH combination"""

load("@rules_go//go:def.bzl", "go_binary")
load("platforms.def.bzl", "GO_ARCH_TO_PLATFORM", "GO_OS_TO_PLATFORM")
load("stamp.bzl", "STAMP_X_DEFS")

# Function to create a go_binary target for each platform-arch combination
def cross_compile_binary(name, goos, goarch):
    # Map Go OS name to Bazel platform OS name
    platform_os = GO_OS_TO_PLATFORM.get(goos, goos)
    # Map Go arch name to Bazel platform arch name
    platform_arch = GO_ARCH_TO_PLATFORM.get(goarch, goarch)

    # Create platform constraint selectors
    constraint_os = "@platforms//os:" + platform_os
    constraint_arch = "@platforms//cpu:" + platform_arch

    # Linux binaries are pure/static (deployed to distroless/scratch). darwin
    # must use cgo: tailscale's certstore links CoreFoundation/Security on macOS,
    # so these build only on a macOS host and are not statically linked.
    pure = "off" if goos == "darwin" else "on"

    # cgo targets cannot be analyzed at all on a host without a C++ toolchain
    # for their platform: rules_go's goos/goarch attrs transition the target
    # platform, so target_compatible_with is satisfied and go_context() then
    # fails hard. Tag them "manual" so wildcards (//..., :all) skip them; CI
    # and humans build them by explicit label on a macOS host.
    tags = ["manual"] if pure == "off" else []

    go_binary(
        name = "{}-{}-{}".format(name, goos, goarch),
        embed = [":{}_lib".format(name)],
        goarch = goarch,  # Use Go's architecture naming for the compiler
        goos = goos,      # Use Go's OS naming for the compiler
        pure = pure,
        tags = tags,
        # This ensures the target is only built when compatible
        target_compatible_with = [
            constraint_os,
            constraint_arch,
        ],
        visibility = ["//visibility:public"],
        x_defs = STAMP_X_DEFS,
    )
