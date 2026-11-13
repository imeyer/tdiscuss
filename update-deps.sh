#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${REPO_ROOT}"

BCR_URL="https://bcr.bazel.build/modules"

GO=go
GH=(gh)
if [[ -n "${GH_TOKEN:-}" ]]; then
  GH=(env GH_TOKEN="${GH_TOKEN}" gh -R imeyer/tdiscuss)
fi

# --- Helper: get latest version of a Bazel module from BCR ---
# Picks the highest non-yanked version by semantic order rather than trusting
# the array's last element (which may be yanked or out of order).
bcr_latest() {
  local module="$1"
  curl -fsSL --retry 3 --retry-delay 2 "${BCR_URL}/${module}/metadata.json" | python3 -c '
import sys, json, re
d = json.load(sys.stdin)
yanked = set((d.get("yanked_versions") or {}).keys())
def key(v):
    return [int(x) if x.isdigit() else x for x in re.split(r"[.\-]", v)]
vs = [v for v in d.get("versions", []) if v not in yanked]
print(sorted(vs, key=key)[-1] if vs else "")
'
}

# Read go.mod directly: Bazel needs its Go SDK to match go.mod, so it cannot
# resolve dependencies while the toolchain update is only partially applied.
if ! module_json=$("${GO}" mod edit -json); then
  echo "error: could not read go.mod" >&2
  exit 1
fi
readarray -t direct_deps < <(
  python3 -c '
import json, sys
for dep in json.loads(sys.argv[1]).get("Require", []):
    if not dep.get("Indirect", False):
        print(dep["Path"])
' "${module_json}"
)
if [[ ${#direct_deps[@]} -eq 0 ]]; then
  echo "error: no direct Go dependencies found in go.mod" >&2
  exit 1
fi

# ============================================================
# 1. Update Go toolchain version
# ============================================================
echo "==> Updating Go toolchain version..."
LATEST_GO=$(curl -fsSL 'https://go.dev/dl/?mode=json' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)[0]['version'].removeprefix('go'))")
if [[ -z "${LATEST_GO}" ]]; then
  echo "error: could not determine latest Go version" >&2
  exit 1
fi

CURRENT_GO=$(awk '/^go /{print $2}' go.mod)
if [[ "${CURRENT_GO}" != "${LATEST_GO}" ]]; then
  echo "    go.mod: ${CURRENT_GO} -> ${LATEST_GO}"
  "${GO}" mod edit -go="${LATEST_GO}"
else
  echo "    already at latest: ${CURRENT_GO}"
fi

if ! grep -q 'go_sdk.download(version = ' MODULE.bazel; then
  echo "error: go_sdk.download not found in MODULE.bazel" >&2
  exit 1
fi
if ! sed -i -E "s/go_sdk\\.download\\(version = \"[^\"]+\"\\)/go_sdk.download(version = \"${LATEST_GO}\")/" MODULE.bazel; then
  echo "error: could not update Go SDK version in MODULE.bazel" >&2
  exit 1
fi

# Update GitHub workflow go-version references (always, in case they drifted)
find .github/workflows \( -name '*.yml' -o -name '*.yaml' \) 2>/dev/null | while read -r f; do
  if grep -qE 'go-version:' "${f}"; then
    sed -i -E "s/(go-version:\s*).*/\1${LATEST_GO}/" "${f}"
    echo "    ${f}: updated go-version"
  fi
done

# ============================================================
# 2. Update bazel_dep() versions in MODULE.bazel
# ============================================================
echo "==> Updating bazel_dep() versions in MODULE.bazel..."

# Parse all bazel_dep lines: extract name and current version
while IFS= read -r line; do
  name=$(echo "${line}" | grep -oP '(?<=\()name\s*=\s*"\K[^"]+')
  current=$(echo "${line}" | grep -oP 'version\s*=\s*"\K[^"]+')

  latest=$(bcr_latest "${name}" 2>/dev/null || true)
  if [[ -z "${latest}" ]]; then
    echo "    ${name}: could not fetch latest version, skipping"
    continue
  fi

  if [[ "${current}" != "${latest}" ]]; then
    echo "    ${name}: ${current} -> ${latest}"
    sed -i "s/bazel_dep(name = \"${name}\", version = \"${current}\"/bazel_dep(name = \"${name}\", version = \"${latest}\"/" MODULE.bazel
  else
    echo "    ${name}: up to date (${current})"
  fi
done < <(grep '^bazel_dep(' MODULE.bazel)

# ============================================================
# 3. Update direct Go dependencies
# ============================================================
echo "==> Updating ${#direct_deps[@]} direct Go dependencies..."
get_args=()
for dep in "${direct_deps[@]}"; do
  echo "    ${dep}"
  get_args+=("${dep}@latest")
done
"${GO}" get "${get_args[@]}"

echo "==> Tidying go.mod..."
"${GO}" mod tidy

# ============================================================
# 4. Update GitHub Actions release tags and their commit pins
# ============================================================
echo "==> Updating GitHub Actions..."

github_action_latest() {
  local action="$1" tag reference sha ref_type
  if ! tag=$("${GH[@]}" api "repos/${action}/releases/latest" --jq '.tag_name'); then
    echo "    ${action}: could not fetch latest release, skipping" >&2
    return 1
  fi
  reference=$("${GH[@]}" api "repos/${action}/git/ref/tags/${tag}")
  sha=$(jq -r '.object.sha' <<<"${reference}")
  ref_type=$(jq -r '.object.type' <<<"${reference}")
  if [[ "${ref_type}" == "tag" ]]; then
    sha=$("${GH[@]}" api "repos/${action}/git/tags/${sha}" --jq '.object.sha')
  elif [[ "${ref_type}" != "commit" ]]; then
    echo "    ${action}: unexpected tag target ${ref_type}, skipping" >&2
    return 1
  fi
  if [[ ! "${sha}" =~ ^[0-9a-f]{40}$ ]]; then
    echo "    ${action}: release tag did not resolve to a commit, skipping" >&2
    return 1
  fi
  printf '%s %s\n' "${sha}" "${tag}"
}

readarray -t actions < <(
  find .github/workflows \( -name '*.yml' -o -name '*.yaml' \) \
    -exec grep -hE '^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+@[0-9a-f]{40}([[:space:]]|$)' {} + \
    | sed -E 's|^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@.*|\2|' \
    | sort -u
)
for action in "${actions[@]}"; do
  if ! latest=$(github_action_latest "${action}"); then
    continue
  fi
  read -r sha tag <<<"${latest}"
  if grep -R -qE "uses:[[:space:]]*${action}@${sha}([[:space:]]|$)" .github/workflows; then
    echo "    ${action}: up to date (${tag})"
    continue
  fi
  find .github/workflows \( -name '*.yml' -o -name '*.yaml' \) \
    -exec sed -i -E "s|(uses:[[:space:]]*${action}@)[0-9a-f]{40}([[:space:]]*#.*)?$|\1${sha} # ${tag}|" {} +
  echo "    ${action}: updated to ${tag} (${sha})"
done

# ============================================================
# 5. Refresh pinned OCI base image digests to their tags
# ============================================================
# oci.pull() pins base images by digest; tags like distroless :nonroot are
# rebuilt (e.g. for CVE fixes), so the pins drift and must be refreshed too.
# No-op until an oci.pull() appears in MODULE.bazel.
echo "==> Refreshing OCI base image digests..."

# registry_digest <image> <tag> -> current manifest (index) digest for the tag.
registry_digest() {
  local image="$1" tag="$2"
  local registry="${image%%/*}" repo="${image#*/}"
  local api="${registry}" token=""
  if [[ "${registry}" == "docker.io" ]]; then
    api="registry-1.docker.io"
    token=$(curl -fsSL "https://auth.docker.io/token?service=registry.docker.io&scope=repository:${repo}:pull" \
      | python3 -c "import sys,json;print(json.load(sys.stdin).get('token',''))")
  fi
  local auth=()
  [[ -n "${token}" ]] && auth=(-H "Authorization: Bearer ${token}")
  curl -fsSI --retry 3 --retry-delay 2 "${auth[@]}" \
    -H "Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json" \
    "https://${api}/v2/${repo}/manifests/${tag}" \
    | tr -d '\r' | awk -F': ' 'tolower($1)=="docker-content-digest"{print $2}'
}

while IFS='|' read -r image tag current; do
  [[ -z "${image}" ]] && continue
  new=$(registry_digest "${image}" "${tag}" || true)
  if [[ -z "${new}" ]]; then
    echo "    ${image}:${tag}: could not fetch digest, skipping"
    continue
  fi
  if [[ "${current}" != "${new}" ]]; then
    echo "    ${image}:${tag}: ${current} -> ${new}"
    sed -i "s|${current}|${new}|" MODULE.bazel
  else
    echo "    ${image}:${tag}: up to date"
  fi
done < <(python3 - <<'PY'
import re
src = open("MODULE.bazel").read()
src = re.sub(r"#.*", "", src)  # strip comments so parens in them can't confuse matching
for m in re.finditer(r"oci\.pull\((.*?)\)", src, re.S):
    b = m.group(1)
    def g(k):
        mm = re.search(k + r'\s*=\s*"([^"]+)"', b)
        return mm.group(1) if mm else ""
    image, tag, digest = g("image"), g("tag"), g("digest")
    if image and tag and digest:
        print(f"{image}|{tag}|{digest}")
PY
)

# ============================================================
# 6. Sync Bazel with updated go.mod / MODULE.bazel
# ============================================================
echo "==> Running bazel mod tidy..."
bazelisk mod tidy --config=silent

echo "==> Done."
