"""Define platform, arch pairs to build"""

GO_PLATFORMS = [
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("darwin", "amd64"),
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
