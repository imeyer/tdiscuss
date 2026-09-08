#!/usr/bin/env bash
# Prints a Tailscale auth key for the dev node on stdout. Everything else goes
# to stderr, so `TS_AUTHKEY=$(./dev-authkey.sh)` works.
#
# Order of preference:
#   1. TS_AUTHKEY from the environment — you brought your own key.
#   2. A fresh ephemeral, pre-approved key minted through the Tailscale API
#      using an OAuth client that holds the dev tag. Supply the client either
#      as TS_API_CLIENT_ID / TS_API_CLIENT_SECRET, or - better - as
#      TS_API_CLIENT_ID_CMD / TS_API_CLIENT_SECRET_CMD, commands whose stdout
#      is the value (e.g. a keyring lookup), so the secret never lives in a
#      file or in the environment of every process you run.
#      See "Automatic tailnet registration" in README.md.
#
# The minted key is single-use and ephemeral: the dev node registers itself on
# every `make dev`, and the tailnet drops the node again once it goes offline.
# That keeps a dev box from accumulating a graveyard of discuss-dev-1, -2, -3.
set -euo pipefail

hostname="${TS_DEV_HOSTNAME:-discuss-dev}"
tags="${TS_DEV_TAGS:-tag:discuss-dev}"
expiry="${TS_DEV_KEY_EXPIRY:-3600}"
tailnet="${TS_TAILNET:--}"

log() { printf '%s\n' "$*" >&2; }

# An optional env file holds the OAuth client (and anything else you want in
# the dev environment). It is plaintext at rest, so refuse to read it if anyone
# but the owner can - a 0644 secret is a secret shared with every local user.
# Values already in the environment win, so `TS_AUTHKEY=... make dev` still
# overrides the file.
env_file="${TDISCUSS_DEV_ENV:-$HOME/.config/tdiscuss/dev.env}"

load_env_file() {
	[[ -f "$env_file" ]] || return 0

	local mode
	mode="$(stat -c '%a' "$env_file" 2>/dev/null || stat -f '%Lp' "$env_file")"
	if (( 8#$mode & 8#077 )); then
		log "$env_file is mode $mode - it holds a secret and must not be readable by anyone else."
		log "Fix it with: chmod 600 $env_file"
		exit 1
	fi

	local vars=(TS_AUTHKEY TS_API_CLIENT_ID TS_API_CLIENT_SECRET
		TS_API_CLIENT_ID_CMD TS_API_CLIENT_SECRET_CMD)
	local var saved=()
	for var in "${vars[@]}"; do
		saved+=("$(eval "printf '%s' \"\${${var}:-}\"")")
	done

	# shellcheck disable=SC1090
	set -a; . "$env_file"; set +a

	local i
	for i in "${!vars[@]}"; do
		if [[ -n "${saved[$i]}" ]]; then
			printf -v "${vars[$i]}" '%s' "${saved[$i]}"
		fi
	done
	log "Loaded $env_file"
}

load_env_file

if [[ -n "${TS_AUTHKEY:-}" ]]; then
	log "Using TS_AUTHKEY from the environment"
	printf '%s\n' "$TS_AUTHKEY"
	exit 0
fi

# Resolve a credential from either VAR or VAR_CMD. The _CMD form is preferred:
# the value exists only for the lifetime of this script, so nothing has to sit
# in a dotfile or leak into the environment of every process in your shell.
resolve_cred() {
	local var="$1" cmd_var="${1}_CMD" cmd value
	cmd="$(eval "printf '%s' \"\${${cmd_var}:-}\"")"
	if [[ -n "$cmd" ]]; then
		if ! value="$(eval "$cmd")"; then
			log "$cmd_var failed: $cmd"
			return 1
		fi
		printf '%s' "${value%%$'\n'*}"
		return 0
	fi
	eval "printf '%s' \"\${${var}:-}\""
}

client_id="$(resolve_cred TS_API_CLIENT_ID)" || exit 1
client_secret="$(resolve_cred TS_API_CLIENT_SECRET)" || exit 1

# Don't hand the secret down to curl (or anything else) through the
# environment; it goes in on stdin below.
unset TS_API_CLIENT_SECRET TS_API_CLIENT_ID

if [[ -z "$client_id" || -z "$client_secret" ]]; then
	cat >&2 <<EOF
No tailnet credentials found. Either bring your own key:

  export TS_AUTHKEY=tskey-auth-...
      https://login.tailscale.com/admin/settings/keys

or let '$hostname' register itself on every run. Three steps, in this order:

  1. Add $tags to tagOwners in your tailnet policy file and save
     it. The next step cannot grant a tag that does not exist yet.
       https://login.tailscale.com/admin/acls/file
  2. Generate an OAuth client, check Write on Keys > Auth Keys, and in the
     tag selector that appears under it pick $tags.
       https://login.tailscale.com/admin/settings/oauth
  3. Put the client where this script can find it:

       make dev-env          # creates a 0600 env file, if you have none
       \$EDITOR $env_file

     and fill in TS_API_CLIENT_ID and TS_API_CLIENT_SECRET. To keep the
     secret out of a plaintext file, set TS_API_CLIENT_SECRET_CMD instead,
     to a command that prints it:

       TS_API_CLIENT_SECRET_CMD='secret-tool lookup service tdiscuss-dev key client-secret'

See "Automatic tailnet registration" in README.md for the long version,
including the admin grant you want for the dev board.
EOF
	exit 1
fi

for tool in curl jq; do
	command -v "$tool" >/dev/null || { log "dev-authkey.sh needs $tool on PATH"; exit 1; }
done

api="${TS_API_BASE:-https://api.tailscale.com}"

log "Minting an ephemeral auth key for $hostname ($tags, ${expiry}s)"

# Run curl with its config (and any credentials) on stdin: argv is
# world-readable through /proc/<pid>/cmdline, so `-u id:secret` would hand the
# secret to every user on this machine for the life of the request.
#
# Deliberately not using curl -f: on an error we want Tailscale's response body,
# which says what is actually wrong, instead of just "returned error: 400".
# api_call returns its result in api_body (and, on failure, the API's message in
# api_error) rather than on stdout, so that callers do not have to wrap it in a
# command substitution - a subshell would throw those assignments away.
api_body=""
api_error=""

api_call() {
	local what="$1" config="$2" code response
	shift 2
	api_body=""
	api_error=""

	response="$(printf '%s\n' "$config" | curl -sS --config - -w '\n%{http_code}' "$@")" || {
		log "$what: curl failed"
		return 1
	}
	code="${response##*$'\n'}"
	api_body="${response%$'\n'*}"

	if [[ "$code" != 2* ]]; then
		# Tailscale answers with {"message":"..."} for most failures.
		api_error="$(printf '%s' "$api_body" | jq -re '.message' 2>/dev/null || printf '%s' "$api_body")"
		log "$what: HTTP $code"
		log "  $api_error"
		return 1
	fi
}

api_call 'OAuth token request' "$(printf '%s\n' \
	"url = \"$api/api/v2/oauth/token\"" \
	"data-urlencode = \"client_id=$client_id\"" \
	"data-urlencode = \"client_secret=$client_secret\"")" || exit 1

token="$(printf '%s' "$api_body" | jq -re '.access_token')" || {
	log "OAuth token request: no access_token in the response"
	exit 1
}

# `tag:a,tag:b` -> ["tag:a","tag:b"]
request_body="$(jq -nc \
	--arg tags "$tags" \
	--argjson expiry "$expiry" \
	--arg desc "tdiscuss dev $hostname" \
	'{
		capabilities: {
			devices: {
				create: {
					reusable: false,
					ephemeral: true,
					preauthorized: true,
					tags: ($tags | split(",") | map(gsub("^\\s+|\\s+$"; "")) | map(select(length > 0)))
				}
			}
		},
		expirySeconds: $expiry,
		description: $desc
	}')"

api_call 'auth key request' "$(printf '%s\n' \
	"url = \"$api/api/v2/tailnet/$tailnet/keys\"" \
	"header = \"Authorization: Bearer $token\"" \
	'header = "Content-Type: application/json"')" \
	--request POST --data "$request_body" || {
	# The overwhelmingly common first-run failure, and the message alone does
	# not say which of the two halves is missing.
	if [[ "$api_error" == *tag* ]]; then
		cat >&2 <<EOF

$tags has to be BOTH:

  - present in tagOwners in your tailnet policy file
      https://login.tailscale.com/admin/acls/file
  - granted to this OAuth client, in the tag selector under its
      "Keys > Auth Keys: Write" scope
      https://login.tailscale.com/admin/settings/oauth

An OAuth client's tags are fixed when you generate it, so if the client was
created before the tag existed in the policy file, it holds no tags and this
is the error you get. Check the client's row on the OAuth clients page: it
lists the tags it carries. If that list is empty or lacks $tags, add the
tag to the policy file first, then delete the client and generate a new one.

To use a tag your client already holds instead: make dev DEV_TAGS=tag:something
EOF
	fi
	exit 1
}

printf '%s' "$api_body" | jq -re '.key' || {
	log "auth key request: no key in the response"
	exit 1
}
