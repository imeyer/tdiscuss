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
TARGET := tdiscuss

# Bazel build command
BAZEL := bazelisk
BAZEL_BUILD_ARGS := build --config=silent --workspace_status_command="$${PWD}/status.sh"
BAZEL_RELEASE_ARGS := build --config=silent --stamp --workspace_status_command="$${PWD}/status.sh"
BAZEL_TEST_ARGS := test --config=silent --build_tests_only
BAZEL_RUN_ARGS := run
# STAMP_X_DEFS puts go_binary behind a stamp transition, so the output lands in
# bazel-out/<cpu>-<mode>-ST-<hash>/bin, NOT the bazel-out/<cpu>-<mode>/bin that
# `bazel info bazel-bin` reports. Ask for the configured target's actual output
# file instead of assembling the path by hand -- otherwise `make dev` silently
# runs whatever stale binary is left in the un-transitioned directory.
BAZEL_QUERY_BIN := cquery --config=silent \
	--workspace_status_command="$${PWD}/status.sh" --output=files //:$(TARGET)
# Change the hostname to anything you wish to use for testing
BAZEL_RUN_TRAILING_ARGS = -hostname $(DEV_HOSTNAME) -debug

# --- Dev environment -------------------------------------------------------
# The node this repo registers on your tailnet when you `make dev`.
DEV_HOSTNAME ?= discuss-dev
# tsnet state (node key, certs) lives here so a dev node never shares state
# with a production install under ~/.config.
DEV_DATA_DIR ?= $(CURDIR)/.dev/$(DEV_HOSTNAME)
# Tag applied to the dev node. Must exist in your tailnet policy file and be
# owned by the OAuth client that mints the key.
DEV_TAGS ?= tag:discuss-dev
DEV_KEY_EXPIRY ?= 3600
# Dev database. `make dev` starts this container unless DATABASE_URL already
# points somewhere - set that (in the env file or the environment) to use a
# Postgres you manage yourself and none of this runs.
DEV_DB_ENGINE ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)
DEV_DB_IMAGE ?= docker.io/library/postgres:17-alpine
DEV_DB_CONTAINER ?= tdiscuss-dev-db
DEV_DB_VOLUME ?= tdiscuss-dev-db-data
# Not 5432: a dev container should not fight a Postgres you already run.
DEV_DB_PORT ?= 5433
DEV_DB_USER ?= discuss
DEV_DB_NAME ?= discuss
DEV_DATABASE_URL ?= postgres://$(DEV_DB_USER)@127.0.0.1:$(DEV_DB_PORT)/$(DEV_DB_NAME)?sslmode=disable
# Optional 0600 file holding the OAuth client and any dev overrides. Loaded by
# both `make dev` and dev-authkey.sh; kept outside the repo so it cannot be
# committed by accident.
DEV_ENV_FILE ?= $(HOME)/.config/tdiscuss/dev.env
DEV_ARGS := -hostname $(DEV_HOSTNAME) -debug

.PHONY: help all clean clean-db test run run-binary genhtml release coverage \
	check-go-versions build dev dev-authkey dev-clean dev-env dev-db \
	dev-db-stop dev-db-reset dev-db-psql dev-admin

.DEFAULT_GOAL := help

help: ## Show this help
	@echo "tdiscuss — make targets"
	@echo
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Dev environment:"
	@echo "  hostname      $(DEV_HOSTNAME)"
	@echo "  state dir     $(DEV_DATA_DIR)"
	@echo "  node tag      $(DEV_TAGS)"
	@echo "  database      $${DATABASE_URL:-$(DEV_DATABASE_URL) (container)}"
	@echo
	@echo "'make dev' registers that node on your tailnet. It needs either"
	@echo "TS_AUTHKEY, or an OAuth client with write access to Keys > Auth Keys"
	@echo "holding $(DEV_TAGS), as TS_API_CLIENT_ID plus either"
	@echo "TS_API_CLIENT_SECRET or TS_API_CLIENT_SECRET_CMD (keyring lookup)."
	@echo "Run 'make dev-authkey' for the setup steps. Details: README.md."

all: check-go-versions test build

build: ## Build the binary for this platform
	@echo "Building for $(PLATFORM)-$(ARCH)"
	$(BAZEL) $(BAZEL_BUILD_ARGS) //:$(TARGET)

test: ## Run all tests
	@echo "Testing all targets"
	$(BAZEL) $(BAZEL_TEST_ARGS) //...

dev: build ## Register the dev node on your tailnet and run it
	@set -eu; \
	mkdir -p "$(DEV_DATA_DIR)"; \
	if [ -f "$(DEV_ENV_FILE)" ]; then set -a; . "$(DEV_ENV_FILE)"; set +a; fi; \
	if [ -z "$${DATABASE_URL:-}" ]; then \
		$(MAKE) --no-print-directory dev-db; \
		DATABASE_URL="$(DEV_DATABASE_URL)"; \
	else \
		echo "Using DATABASE_URL from your environment"; \
	fi; \
	export DATABASE_URL; \
	key="$$(TS_DEV_TAGS='$(DEV_TAGS)' TS_DEV_KEY_EXPIRY='$(DEV_KEY_EXPIRY)' \
		TS_DEV_HOSTNAME='$(DEV_HOSTNAME)' ./dev-authkey.sh)"; \
	bin="$$($(BAZEL) $(BAZEL_QUERY_BIN) 2>/dev/null)"; \
	if [ -z "$$bin" ] || [ ! -x "$$bin" ]; then \
		echo "Could not resolve the built binary for //:$(TARGET)." >&2; \
		echo "Run 'make build' first, or check 'bazelisk $(BAZEL_QUERY_BIN)'." >&2; \
		exit 1; \
	fi; \
	echo "Running $(DEV_HOSTNAME) from $$bin"; \
	exec env \
		DATA_DIR="$(DEV_DATA_DIR)" \
		TSNET_HOSTNAME="$(DEV_HOSTNAME)" \
		TS_AUTHKEY="$$key" \
		DATABASE_URL="$$DATABASE_URL" \
		"$$bin" $(DEV_ARGS)

dev-authkey: ## Mint (and print) a tailnet auth key for the dev node
	@TS_DEV_TAGS='$(DEV_TAGS)' TS_DEV_KEY_EXPIRY='$(DEV_KEY_EXPIRY)' \
		TS_DEV_HOSTNAME='$(DEV_HOSTNAME)' ./dev-authkey.sh

dev-db: ## Start the dev PostgreSQL container and load the schema
	@set -eu; \
	engine="$(DEV_DB_ENGINE)"; \
	if [ -z "$$engine" ]; then \
		echo "No podman or docker found. Either install one, or point DATABASE_URL"; \
		echo "at a PostgreSQL 17+ you run yourself - see README.database-setup.md."; \
		exit 1; \
	fi; \
	if [ -z "$$($$engine ps -q --filter name=^$(DEV_DB_CONTAINER)$$)" ]; then \
		if [ -n "$$($$engine ps -aq --filter name=^$(DEV_DB_CONTAINER)$$)" ]; then \
			echo "Starting $(DEV_DB_CONTAINER)"; \
			$$engine start $(DEV_DB_CONTAINER) >/dev/null; \
		else \
			echo "Creating $(DEV_DB_CONTAINER) from $(DEV_DB_IMAGE) on 127.0.0.1:$(DEV_DB_PORT)"; \
			$$engine run -d --name $(DEV_DB_CONTAINER) \
				-p 127.0.0.1:$(DEV_DB_PORT):5432 \
				-e POSTGRES_USER=$(DEV_DB_USER) \
				-e POSTGRES_DB=$(DEV_DB_NAME) \
				-e POSTGRES_HOST_AUTH_METHOD=trust \
				-v $(DEV_DB_VOLUME):/var/lib/postgresql/data \
				$(DEV_DB_IMAGE) >/dev/null; \
		fi; \
	fi; \
	ok=; \
	printf 'Waiting for postgres'; \
	for _ in $$(seq 1 60); do \
		if $$engine exec $(DEV_DB_CONTAINER) pg_isready -q -U $(DEV_DB_USER) -d $(DEV_DB_NAME) 2>/dev/null; then ok=1; break; fi; \
		printf '.'; sleep 1; \
	done; \
	echo; \
	if [ -z "$$ok" ]; then \
		echo "postgres never became ready. Logs: $$engine logs $(DEV_DB_CONTAINER)"; \
		exit 1; \
	fi; \
	if ! $$engine exec $(DEV_DB_CONTAINER) psql -qtAX -U $(DEV_DB_USER) -d $(DEV_DB_NAME) \
			-c "select to_regclass('public.board_data') is not null" | grep -q '^t$$'; then \
		echo "Loading sqlc/schema.sql"; \
		$$engine exec -i $(DEV_DB_CONTAINER) \
			psql -q -v ON_ERROR_STOP=1 -U $(DEV_DB_USER) -d $(DEV_DB_NAME) < sqlc/schema.sql; \
	fi; \
	echo "Database ready: $(DEV_DATABASE_URL)"

# An empty board has no admin: there is no automatic first-user promotion, and
# a capability grant written for the production tag does not apply to the dev
# node. This sets the member.is_admin column, the other half of the union.
dev-admin: ## Grant board admin in the dev database (EMAIL=..., or the only member)
	@set -eu; \
	engine="$(DEV_DB_ENGINE)"; \
	psql="exec $(DEV_DB_CONTAINER) psql -qtAX -U $(DEV_DB_USER) -d $(DEV_DB_NAME)"; \
	email='$(EMAIL)'; \
	if [ -z "$$email" ]; then \
		count="$$($$engine $$psql -c 'select count(*) from member')"; \
		if [ "$$count" != 1 ]; then \
			echo "$$count members in the dev board - name one with EMAIL=..."; \
			$$engine $$psql -c 'select email from member order by id' | sed 's/^/  /'; \
			exit 1; \
		fi; \
		email="$$($$engine $$psql -c 'select email from member')"; \
	fi; \
	: 'psql expands :vars only for SQL it reads from a file or stdin, not -c'; \
	updated="$$(printf '%s\n' \
		"update member set is_admin = true where email = :'email' returning email;" \
		| $$engine exec -i $(DEV_DB_CONTAINER) psql -qtAX -v ON_ERROR_STOP=1 \
			-v email="$$email" -U $(DEV_DB_USER) -d $(DEV_DB_NAME))"; \
	if [ -z "$$updated" ]; then \
		echo "No member with that email. They have to load the board once for the row to exist."; \
		exit 1; \
	fi; \
	echo "$$updated is now a board admin. Reload the page."

dev-db-stop: ## Stop the dev database container (data is kept)
	@set -eu; \
	engine="$(DEV_DB_ENGINE)"; \
	if [ -n "$$engine" ] && [ -n "$$($$engine ps -q --filter name=^$(DEV_DB_CONTAINER)$$)" ]; then \
		$$engine stop $(DEV_DB_CONTAINER) >/dev/null && echo "Stopped $(DEV_DB_CONTAINER)"; \
	else \
		echo "$(DEV_DB_CONTAINER) is not running"; \
	fi

dev-db-psql: ## Open a psql shell on the dev database
	@$(DEV_DB_ENGINE) exec -it $(DEV_DB_CONTAINER) psql -U $(DEV_DB_USER) -d $(DEV_DB_NAME)

dev-db-reset: ## Destroy the dev database (container and data) and rebuild it
	@set -eu; \
	engine="$(DEV_DB_ENGINE)"; \
	if [ -n "$$engine" ]; then \
		$$engine rm -f $(DEV_DB_CONTAINER) >/dev/null 2>&1 || true; \
		$$engine volume rm -f $(DEV_DB_VOLUME) >/dev/null 2>&1 || true; \
		echo "Removed $(DEV_DB_CONTAINER) and its data"; \
	fi
	@$(MAKE) --no-print-directory dev-db

dev-env: ## Create the mode-0600 env file for the dev credentials
	@set -eu; \
	if [ -e "$(DEV_ENV_FILE)" ]; then \
		echo "$(DEV_ENV_FILE) already exists (mode $$(stat -c '%a' "$(DEV_ENV_FILE)"))"; \
		exit 0; \
	fi; \
	mkdir -p "$$(dirname "$(DEV_ENV_FILE)")"; \
	umask 077; \
	printf '%s\n' \
		'# tdiscuss dev environment. Loaded by `make dev` and dev-authkey.sh.' \
		'# Holds an OAuth client secret: keep this file mode 0600. Both refuse' \
		'# to read it if it is readable by anyone else.' \
		'' \
		'# OAuth client from https://login.tailscale.com/admin/settings/oauth' \
		'# (Keys > Auth Keys: Write, granted $(DEV_TAGS))' \
		'TS_API_CLIENT_ID=' \
		'TS_API_CLIENT_SECRET=' \
		'' \
		'# Or skip the OAuth client and paste a key you made by hand:' \
		'#TS_AUTHKEY=' \
		'' \
		'# Uncommenting DATABASE_URL opts out of the dev database container:' \
		'# make dev then uses your Postgres and leaves containers alone.' \
		'#DATABASE_URL=postgres://discuss@localhost:5432/discuss' \
		> "$(DEV_ENV_FILE)"; \
	chmod 600 "$(DEV_ENV_FILE)"; \
	echo "Created $(DEV_ENV_FILE) (mode 0600)."; \
	echo "Fill in TS_API_CLIENT_ID and TS_API_CLIENT_SECRET, then run 'make dev'."

dev-clean: ## Delete the dev node's local tsnet state
	@rm -rf "$(DEV_DATA_DIR)" && echo "Removed $(DEV_DATA_DIR)"
	@echo "The node itself is ephemeral; your tailnet drops it once it goes offline."

run: ## Run via 'bazel run' (uses your ambient DATABASE_URL/TS_AUTHKEY)
	@echo "Running for $(PLATFORM)-$(ARCH) from $(BAZEL)"
	$(BAZEL) $(BAZEL_RUN_ARGS) //:$(TARGET) -- $(BAZEL_RUN_TRAILING_ARGS)

run-binary: ## Run the already-built binary (ambient DATABASE_URL/TS_AUTHKEY)
	@set -eu; \
	bin="$$($(BAZEL) $(BAZEL_QUERY_BIN) 2>/dev/null)"; \
	if [ -z "$$bin" ] || [ ! -x "$$bin" ]; then \
		echo "Could not resolve the built binary for //:$(TARGET). Run 'make build' first." >&2; \
		exit 1; \
	fi; \
	echo "Running for $(PLATFORM)-$(ARCH) from $$bin"; \
	exec "$$bin" $(BAZEL_RUN_TRAILING_ARGS)

release: ## Build a stamped release binary
	@echo "Building release for $(PLATFORM)-$(ARCH)"
	$(BAZEL) $(BAZEL_RELEASE_ARGS) //:$(TARGET)

coverage: ## Run tests with a combined lcov coverage report
	@echo "Generating coverage for //..."
	$(BAZEL) coverage --combined_report=lcov //...

genhtml: ## Render the coverage report as HTML into genhtml/
	@echo "Generating HTML report for coverage"
	@[ -d "$(shell pwd)/genhtml" ] && rm -rf "$(shell pwd)/genhtml" && echo "Removed previous genhtml/"
	@genhtml --branch-coverage --output genhtml "$(shell $(BAZEL) info output_path)/_coverage/_coverage_report.dat" 2>&1>/dev/null

clean-db: ## Reset a host-installed (non-container) discuss database
	@dropdb -U discuss discuss
	@createdb -U discuss discuss
	@psql -U discuss discuss < sqlc/schema.sql

clean: ## bazel clean
	$(BAZEL) clean

check-go-versions: ## Check the Go versions across build files agree
	@bash check-go-versions.sh
