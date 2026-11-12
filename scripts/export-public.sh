#!/usr/bin/env bash
# 导出可公开的干净副本（无历史）：只取 HEAD 已提交内容中命中 scripts/public-allowlist.txt 的文件。
# 不读工作树，未提交的改动不会被导出。
#
#   ./scripts/export-public.sh <out-dir>     # out-dir 不存在或为空
set -euo pipefail
cd "$(dirname "$0")/.."
out="${1:?usage: export-public.sh <out-dir>}"
mkdir -p "$out"
[ -z "$(ls -A "$out")" ] || { echo "error: $out 非空" >&2; exit 1; }

paths=()
while IFS= read -r line; do
  case "$line" in ''|\#*) ;; *) paths+=("$line") ;; esac
done < scripts/public-allowlist.txt
git archive HEAD -- "${paths[@]}" | tar -x -C "$out"
echo "exported $(find "$out" -type f | wc -l | tr -d ' ') files to $out"
