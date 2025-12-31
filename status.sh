#!/usr/bin/env bash

git_sha=$(git rev-parse --short HEAD)
if [[ $? != 0 ]]; then
    exit 1
fi

# Use GITHUB_REF_NAME in CI (set to tag on tag push), otherwise git describe
if [[ -n "${GITHUB_REF_NAME:-}" && "${GITHUB_REF_NAME}" == v* ]]; then
    version="${GITHUB_REF_NAME}"
else
    version=$(git describe --tags 2>/dev/null || echo "dev")
fi

echo "STABLE_GIT_SHA ${git_sha}"
echo "STABLE_VERSION ${version}"
