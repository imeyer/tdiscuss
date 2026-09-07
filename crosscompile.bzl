"""Macro for generating cross-platform builds"""

load("helper.bzl", "cross_compile_binary")
load("platforms.def.bzl", "GO_PLATFORMS")

def create_cross_platform_targets(binary_name):
    """Creates platform-specific targets for the given binary name

    Args:
      binary_name: base name of the binary; each target is named
        "<binary_name>-<goos>-<goarch>" and embeds ":<binary_name>_lib".
    """
    for goos, goarch in GO_PLATFORMS:
        cross_compile_binary(
            name = binary_name,
            goos = goos,
            goarch = goarch,
        )
