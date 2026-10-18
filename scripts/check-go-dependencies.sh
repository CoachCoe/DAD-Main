#!/usr/bin/env bash
set -euo pipefail

packages="$(go list -deps -test ./...)"
while IFS= read -r package; do
  case "$package" in
    golang.org/x/crypto/openpgp | golang.org/x/crypto/openpgp/*)
      printf 'Unsafe dependency: %s (GO-2026-5932)\n' "$package" >&2
      exit 1
      ;;
  esac
done <<< "$packages"
printf '%s\n' 'Go dependency check passed: legacy OpenPGP is absent.'
