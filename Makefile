# Detect OS
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
	PLATFORM := darwin
else ifeq ($(UNAME_S),Linux)
	PLATFORM := linux
else
	$(error Unsupported platform: $(UNAME_S))
endif

SUDO_C := $(shell which sudo)

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
BAZEL_BUILD_ARGS := build --config=silent --workspace_status_command="$${PWD}/status.sh"
BAZEL_RELEASE_ARGS := build --config=silent --stamp --workspace_status_command="$${PWD}/status.sh"
BAZEL_TEST_ARGS := test --config=silent --build_tests_only --test_output=errors
BAZEL_RUN_ARGS := run
# Change the hostname to anything you wish to use for testing
BAZEL_RUN_TRAILING_ARGS := -hostname discuss-dev -debug

.PHONY: all clean test run run-binary genhtml release coverage setup-db

all: test build

build:
	@echo "Building for $(PLATFORM)-$(ARCH)"
	$(BAZEL) $(BAZEL_BUILD_ARGS) //:$(TARGET)

test:
	@echo "Testing all targets"
	$(BAZEL) $(BAZEL_TEST_ARGS) //...

run:
	@echo "Running for $(PLATFORM)-$(ARCH) from $(BAZEL)"
	$(BAZEL) $(BAZEL_RUN_ARGS) //:$(TARGET) -- $(BAZEL_RUN_TRAILING_ARGS)

run-binary:
	@echo "Running for $(PLATFORM)-$(ARCH) from $(shell $(BAZEL) info bazel-bin)"
	$(shell $(BAZEL) info bazel-bin)/$(TARGET)_/$(TARGET) $(BAZEL_RUN_TRAILING_ARGS)

release:
	@echo "Building release for $(PLATFORM)-$(ARCH)"
	$(BAZEL) $(BAZEL_RELEASE_ARGS) //:$(TARGET)

coverage:
	@echo "Generating coverage for //..."
	$(BAZEL) coverage --combined_report=lcov //...

genhtml:
	@echo "Generating HTML report for coverage"
	@[ -d "$(shell pwd)/genhtml" ] && rm -rf "$(shell pwd)/genhtml" && echo "Removed previous genhtml/"
	@genhtml --branch-coverage --output genhtml "$(shell $(BAZEL) info output_path)/_coverage/_coverage_report.dat" 2>&1>/dev/null

clean-db:
	@dropdb -U discuss discuss
	@createdb -U discuss discuss
	@psql -U discuss discuss < sqlc/schema.sql

clean:
	$(BAZEL) clean
