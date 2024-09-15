# Detect OS
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
    PLATFORM := darwin
else ifeq ($(UNAME_S),Linux)
    PLATFORM := linux
else
    $(error Unsupported platform: $(UNAME_S))
endif

# Detect architecture
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_M),arm64)
    ARCH := arm64
else ifeq ($(UNAME_M),x86_64)
    ARCH := amd64
else
    $(error Unsupported architecture: $(UNAME_M))
endif

# Combine platform and architecture
TARGET := tdiscuss-$(PLATFORM)-$(ARCH)

# Bazel build command
BAZEL := bazelisk
BAZEL_ARGS := build --stamp --workspace_status_command="$${PWD}/status.sh"

.PHONY: all clean

all: build

build:
	@echo "Building for $(PLATFORM)-$(ARCH)"
	$(BAZEL) $(BAZEL_ARGS) //:$(TARGET)

clean:
	$(BAZEL) clean
