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
BAZEL_BUILD_ARGS := build --config=silent --stamp --workspace_status_command="$${PWD}/status.sh"
BAZEL_TEST_ARGS := test --config=silent --build_tests_only --test_output=errors
BAZEL_RUN_ARGS := run
# Change the hostname to anything you wish to use for testing
BAZEL_RUN_TRAILING_ARGS := -- -hostname discuss-dev

.PHONY: all clean

all: build

build:
	@echo "Building for $(PLATFORM)-$(ARCH)"
	$(BAZEL) $(BAZEL_BUILD_ARGS) //:$(TARGET)

test:
	@echo "Testing all targets"
	$(BAZEL) $(BAZEL_TEST_ARGS) //...

run:
	@echo "Running for $(PLATFORM)-$(ARCH)"
	$(BAZEL) $(BAZEL_RUN_ARGS) //:$(TARGET) $(BAZEL_RUN_TRAILING_ARGS)

clean:
	$(BAZEL) clean
