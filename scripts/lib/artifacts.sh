#!/usr/bin/env bash
# Shared artifact path conventions for promshim harness scripts.
# Expects REPO_ROOT to be set by the caller.
# PROM_SHIM_ARTIFACT_ROOT is repo-relative; default: harness/artifacts.

artifact_root_rel() {
  local root="${PROM_SHIM_ARTIFACT_ROOT:-harness/artifacts}"
  root="${root#/}"
  root="${root%/}"
  printf '%s' "$root"
}

artifact_root_abs() {
  printf '%s/%s' "$REPO_ROOT" "$(artifact_root_rel)"
}

artifact_rel() {
  local suffix="$1"
  local root
  root="$(artifact_root_rel)"
  suffix="${suffix#/}"
  printf '%s/%s' "$root" "$suffix"
}

artifact_abs() {
  local suffix="$1"
  suffix="${suffix#/}"
  printf '%s/%s' "$(artifact_root_abs)" "$suffix"
}
