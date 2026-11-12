#!/usr/bin/env bash
# 输出当前工作树（含未跟踪、遵守 .gitignore）的 tree 哈希：临时 index 先载入 HEAD（保留被 .gitignore 命中的已跟踪文件），
# 再 `git add -A` 与 `git write-tree`，结果等于真实 `git add -A` 后的 `git write-tree`，且不碰真实 index。供 make check 写入 stamp、scripts/commit.sh 与暂存内容的 tree 比对，一致则跳过重复检查。
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
idx=$(mktemp "${TMPDIR:-/tmp}/pax-index.XXXXXX")
trap 'rm -f "$idx"' EXIT
rm -f "$idx"
GIT_INDEX_FILE=$idx git read-tree HEAD
GIT_INDEX_FILE=$idx git add -A
GIT_INDEX_FILE=$idx git write-tree
