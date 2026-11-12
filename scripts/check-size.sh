#!/usr/bin/env bash
# 文件规模门禁：项目维护的文本文件不得超过 20,000 字节（约 5,000 token）。
#
#   scripts/check-size.sh                 手动检查暂存文件，读取 Git 索引中的完整内容
#   scripts/check-size.sh --changed       手动检查工作区 + 暂存 + 未跟踪（非忽略）相对 HEAD 的变更，读取工作区内容（make lint / make check 也调用此模式）
#   scripts/check-size.sh --baseline REV  按 REV 重新生成基线内容（输出到 stdout），用于重建 scripts/size-baseline.txt
#
# 规则（原 AGENTS.md「文件规模」）：
#   - 以完整文件的实际字节数判断；部分暂存时，工作区检查不能代替暂存检查，所以默认模式读索引。
#   - 跳过删除项与二进制文件（只管文本）。
#   - 排除项只限下方 EXCLUDE 列表；新增排除须有生成来源或项目配置依据，并写在列表里。
#   - 存量超限文件：只允许缩小或保持原大小（基线见 scripts/size-baseline.txt，头部记录来源提交）。
#     重命名沿用原路径的基线；降到限额内后恢复普通限制（基线条目可留着，不再起作用）。
#   - 缺少可信基线的超限文件一律按违规处理，不能默认放行。
#   - 拆分方式：按职责拆分并命名，保持公开行为、依赖方向和状态归属，更新引用与测试；
#     私有模块无需多个调用方；不得通过压行、删除必要注释或机械分片规避限制。
#   - 违规报告须列出：路径、实际字节数、适用限额、建议拆出的职责和目标文件（本脚本输出前三项，
#     后两项由实施者补齐）。硬性限额未满足、或基线未验证时，报告未通过及待处理项；
#     额外例外须用户明确同意（脚本无豁免开关，也不应加）。
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

LIMIT=20000
BASELINE_FILE=scripts/size-baseline.txt

# 排除项（路径 glob，case 语法；`*` 可跨目录）。每项写明依据。
is_excluded() {
  case "$1" in
    # 锁文件：包管理器生成，不手写。
    */go.sum|go.sum|*Package.resolved|*.lock|*package-lock.json|*pnpm-lock.yaml|*yarn.lock) return 0 ;;
    # 明确的自动生成代码：scripts/gen-proto.sh 的产物（头部均为 "Code generated ... DO NOT EDIT"）。
    #   Go：daemon/gen/proto/**/*.pb.go、*_grpc.pb.go（及 tests/s0-ipc 的同类产物）
    #   Swift：macos/Packages/PaxProto/Sources/PaxProto/Generated/（*.pb.swift、*.grpc.swift）
    *.pb.go|*/Generated/*) return 0 ;;
    # 第三方代码：third_party/ 存放上游许可证全文（GPL-3.0 等），非本项目撰写。
    third_party/*) return 0 ;;
  esac
  return 1
}

# 二进制判定：前 8000 字节含 NUL 即视为二进制（与 git 的启发式一致）。
# 从 stdin 读。读完剩余输入，避免上游收到 SIGPIPE 使 pipefail 下的管道状态失真。
is_binary() {
  local nul
  nul=$(head -c 8000 | LC_ALL=C tr -cd '\000' | wc -c) || true
  cat >/dev/null
  [ "${nul:-0}" -gt 0 ]
}

baseline_of() { # <path> -> 基线字节数，无则空
  [ -f "$BASELINE_FILE" ] || return 0
  awk -v p="$1" '/^#/ {next} { b=$1; sub(/^[0-9]+ /,""); if ($0==p) {print b; exit} }' "$BASELINE_FILE"
}

mode=staged
case "${1:-}" in
  "") ;;
  --changed) mode=changed ;;
  --baseline)
    rev=${2:?"用法: --baseline REV"}
    echo "# 存量超限文件基线：来源提交 $(git rev-parse --short "$rev")；格式 <字节数> <路径>"
    echo "# 范围：该提交中非排除项、>${LIMIT} 字节的文本文件。重建：scripts/check-size.sh --baseline <REV>"
    git ls-tree -r -l -z "$rev" | while IFS=$'\t' read -r -d '' meta path; do
      size=$(awk '{print $4}' <<<"$meta")
      [ "$size" != "-" ] && [ "$size" -gt "$LIMIT" ] || continue
      is_excluded "$path" && continue
      if git cat-file blob "$(awk '{print $3}' <<<"$meta")" | is_binary; then continue; fi
      echo "$size $path"
    done | LC_ALL=C sort -k2
    exit 0 ;;
  *) echo "用法: $0 [--changed | --baseline REV]" >&2; exit 2 ;;
esac

# 收集候选："<路径>\t<旧路径或空>"
candidates=$(mktemp); trap 'rm -f "$candidates"' EXIT
if [ "$mode" = staged ]; then
  diff_args=(--cached)
else
  diff_args=(HEAD)
fi
# --name-status -z：普通项 "S\0path\0"；重命名/复制 "Rxx\0old\0new\0"。--diff-filter=d 跳过删除项。
git diff "${diff_args[@]}" -M --name-status -z --diff-filter=d | {
  while IFS= read -r -d '' status; do
    case "$status" in
      R*|C*) IFS= read -r -d '' old; IFS= read -r -d '' new; printf '%s\t%s\n' "$new" "$old" ;;
      *) IFS= read -r -d '' path; printf '%s\t\n' "$path" ;;
    esac
  done
} >"$candidates"
if [ "$mode" = changed ]; then
  git ls-files -o --exclude-standard -z | while IFS= read -r -d '' path; do printf '%s\t\n' "$path"; done >>"$candidates"
fi

violations=0; checked=0
while IFS=$'\t' read -r path old; do
  [ -n "$path" ] || continue
  is_excluded "$path" && continue
  if [ "$mode" = staged ]; then
    size=$(git cat-file -s ":$path"); reader=(git cat-file blob ":$path")
  else
    [ -f "$path" ] || continue   # 符号链接目标缺失、子模块等
    size=$(wc -c <"$path" | tr -d ' '); reader=(cat "$path")
  fi
  checked=$((checked + 1))
  [ "$size" -gt "$LIMIT" ] || continue
  if "${reader[@]}" | is_binary; then continue; fi
  base=$(baseline_of "$path")
  [ -n "$base" ] || { [ -z "$old" ] || base=$(baseline_of "$old"); }
  if [ -z "$base" ]; then
    echo "超限：$path  实际 ${size} 字节  限额 ${LIMIT} 字节（无基线；按职责拆分到新模块）" >&2
    violations=$((violations + 1))
  elif [ "$size" -gt "$base" ]; then
    echo "存量超限文件增大：$path  实际 ${size} 字节  基线 ${base} 字节  限额 ${LIMIT} 字节（新增职责放入合适的新模块）" >&2
    violations=$((violations + 1))
  fi
done <"$candidates"

if [ "$violations" -gt 0 ]; then
  echo "check-size: ${violations} 项违规（模式：${mode}，检查 ${checked} 个文件）" >&2
  exit 1
fi
echo "check-size: 通过（模式：${mode}，检查 ${checked} 个文件）"
