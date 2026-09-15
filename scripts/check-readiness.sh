#!/bin/sh

set -eu

repo_dir="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

if [ -f "$repo_dir/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$repo_dir/.env"
  set +a
fi

cd "$repo_dir"
build_dir="$(mktemp -d)"
trap 'rm -rf "$build_dir"' EXIT HUP INT TERM

go build -o "$build_dir/readiness" ./cmd/readiness

set +e
"$build_dir/readiness" "$@" observability-contract.yaml
status=$?
set -e

exit "$status"
