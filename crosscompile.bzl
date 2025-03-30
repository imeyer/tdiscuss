"""Macro for generating cross-platform builds"""

load("@rules_go//go:def.bzl", "go_binary")
load("helper.bzl", "cross_compile_binary")
load("platforms.def.bzl", "GO_PLATFORMS")

def create_cross_platform_targets(name, embed):
    """Creates platform-specific targets for the given binary name"""
    # Create platform-specific binaries
    for goos, goarch in GO_PLATFORMS:
        cross_compile_binary(
            name = name,
            goos = goos,
            goarch = goarch,
        )
