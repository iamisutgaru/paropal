#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC_DIR="${ROOT}/static/sjb"
OUT_FILE="${ROOT}/static/sjb.tar.gz"

if [[ ! -d "${SRC_DIR}" ]]; then
  echo "missing source directory: ${SRC_DIR}" >&2
  exit 1
fi

mkdir -p "$(dirname "${OUT_FILE}")"

tar -C "${ROOT}/static" -czf "${OUT_FILE}" sjb
