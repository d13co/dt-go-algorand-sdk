#!/usr/bin/env bash
# dt-go-algorand-sdk: checks that the fork's changes to upstream code stay
# small, so that upstream releases merge cleanly. Fork code lives in dt_*.go
# files; the only other files the fork may change are listed below, plus the
# types that go-algorand's scripts/export_sdk_types.py exports from
# dt-algorand, so that they match the node's. .dt-base
# holds the upstream commit the fork is built on: update it when merging an
# upstream release.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

base=$(tr -d '[:space:]' < .dt-base)
allowed=(
  .dt-base
  .github/workflows/dtsdk.yml
  DTALGOSDK.md
  README.md
  client/v2/common/common.go
  scripts/dt-guard.sh
  # Exported from dt-algorand by its scripts/export_sdk_types.py.
  protocol/config/config.go
  protocol/config/consensus.go
  protocol/consensus.go
  types/applications.go
  types/asset.go
  types/block.go
  types/signature.go
  types/statedelta.go
  types/transaction.go
)
fail=0

# Upstream workflows must only run in upstream's repository.
for wf in .github/workflows/*.yml; do
  [ "$wf" = .github/workflows/dtsdk.yml ] && continue
  allowed+=("$wf")
  if ! grep -q "if: github.repository == 'algorand/go-algorand-sdk'" "$wf"; then
    echo "dt-guard: $wf lacks the upstream repository guard" >&2
    fail=1
  fi
done

while IFS= read -r path; do
  case "$(basename "$path")" in dt_*) continue ;; esac
  ok=0
  for a in "${allowed[@]}"; do
    [ "$path" = "$a" ] && ok=1 && break
  done
  if [ "$ok" = 0 ]; then
    echo "dt-guard: $path differs from upstream ($base) but is not a dt_ file or allowlisted" >&2
    fail=1
  fi
done < <(git diff --name-only "$base" -- .)

# The one edit to upstream code: the ResponseInfo hook in submitFormRaw.
if ! grep -q 'info := resetResponseInfo(ctx)' client/v2/common/common.go; then
  echo "dt-guard: the ResponseInfo hook is missing from client/v2/common/common.go" >&2
  fail=1
fi

if [ "$fail" = 0 ]; then
  echo "dt-guard: ok"
fi
exit "$fail"
