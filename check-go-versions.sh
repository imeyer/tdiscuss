#!/usr/bin/env bash

set -euo pipefail

# Everything should match what's in go.mod
GV=$(grep "^go" go.mod | awk '{print $2}' | tr -d 'v')

# There are only two places where version is defined so far but this should
# catch most others as they're added as 'go-version'
readarray -t versions < <(grep -h -r --include=*.yml --include=*.yaml -E '^\s+go-version' . | \
                          awk '{print $2}' | \
                          tr -d "'" | \
                          uniq)

if [[ "${#versions[@]}" != 1 ]]; then
  printf "error: go-version mismatch in .github/workflows: "
  printf "have(%s), want(%s)\n" "${versions}" "${GV}" && exit 1
fi

if [[ "${GV}" != "${versions[@]}" ]]; then
  printf "error: go-version mismatch in go.mod: "
  printf "have(%s), want(%s)\n" "$GV" "${versions}" && exit 1
fi
