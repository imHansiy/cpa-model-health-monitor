#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION="${VERSION:-0.1.0}"
ARCH="${ARCH:-amd64}"
case "${ARCH}" in amd64|arm64) ;; *) echo "ARCH must be amd64 or arm64" >&2; exit 1;; esac
mkdir -p "${ROOT_DIR}/dist"
TEMP_DIR="$(mktemp -d "${ROOT_DIR}/.build-output.XXXXXX")"
docker buildx build --platform "linux/${ARCH}" --progress plain --build-arg "VERSION=${VERSION}" --build-arg "TARGET_GOARCH=${ARCH}" --target artifact --output "type=local,dest=${TEMP_DIR}" "${ROOT_DIR}"
mv "${TEMP_DIR}/cpa-model-health-monitor.so" "${ROOT_DIR}/dist/cpa-model-health-monitor-linux-${ARCH}.so"
rmdir "${TEMP_DIR}"
echo "Built dist/cpa-model-health-monitor-linux-${ARCH}.so"
