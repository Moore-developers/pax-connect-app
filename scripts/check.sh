#!/usr/bin/env bash
# make check：按变更路径选最小检查集。变更 = `git diff --name-only HEAD`（工作区+暂存）+ 未跟踪（非忽略）。
#   用法：scripts/check.sh [--plan]      （或 CHECK_PLAN=1）只打印选中的步骤，不执行、不写 stamp
#   路径分类（先匹配先生效）：
#     文档  *.md、docs/、.scratch/、tests/*/evidence/ → 不跑测试，只跑 lint-size
#     工具  Makefile、check/commit/worktree-hash/check-size/ui-shot/relaunch 脚本、.githooks/ 等 → 只跑 lint-size + 语法检查
#     Swift 保留测试包 → P 及其有测试的下游；PaxUI / Features / macos App → 静态检查 + 真实路径验收提醒
#           保留测试包依赖链 PaxProto→PaxClient→PaxCore
#           scripts/test-swift.py → Python 语法检查；runner 行为变更另选保留包的定向用例
#     proto api/*.proto → Swift 全部 + Go（等于全量，需先向用户说明再跑）
#     其余（daemon/、patches/、configs/、tests/、其它 scripts/ 等，含未识别路径，保守）→ Go 测试
#   lint：lint-size 总跑；lint-go 仅当涉及 Go（daemon/、patches/、*.go、api/*.proto）；lint-swift 仅当涉及 macos/*.swift。
#   日志：每次运行写 $(git rev-parse --git-path pax-check-logs)/<时间戳>-<pid>.log；失败时在终端列出失败用例名。
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

plan_only=${CHECK_PLAN:-}
[ "${1:-}" != "--plan" ] || plan_only=1

changed_paths() { git diff --name-only -z HEAD; git ls-files -o --exclude-standard -z; }

if [ "${1:-}" = "--syntax" ]; then # 内部步骤：改动的 shell/python 语法检查
  changed_paths | while IFS= read -r -d '' f; do
    [ -f "$f" ] || continue
    case "$f" in
      *.sh|.githooks/*) bash -n "$f" ;;
      *.py) python3 -B -c 'import ast,sys; ast.parse(open(sys.argv[1]).read(), sys.argv[1])' "$f" ;;
    esac
  done
  exit 0
fi

SWIFT_ORDER=(PaxClient PaxCore)
# 选中的包记在变量 hit_<包名>（因何选中），兼容 macOS 自带 bash 3.2（无关联数组）
hit() { local v="hit_$1"; printf %s "${!v:-}"; }
want_go= want_ui_acceptance= want_lint_go= want_lint_swift= want_syntax= count=0 docs=0 tooling=0

mark_swift() { # $1=包名 $2=路径；沿下游传播
  local pkg=$1 path=$2
  [ -z "$(hit "$pkg")" ] || return 0
  printf -v "hit_$pkg" %s "$path"
  case "$pkg" in
    PaxClient) mark_swift PaxCore "$path" ;;
  esac
}

tmp=$(mktemp "${TMPDIR:-/tmp}/pax-check-paths.XXXXXX")
trap 'rm -f "$tmp"' EXIT
changed_paths > "$tmp"
while IFS= read -r -d '' path; do
  count=$((count + 1))
  case "$path" in
    *.md|docs/*|.scratch/*|tests/*/evidence/*) docs=$((docs + 1)) ;;
    Makefile|scripts/check.sh|scripts/commit.sh|scripts/worktree-hash.sh|scripts/check-size.sh|scripts/size-baseline.txt|scripts/ui-shot.sh|scripts/ui-shot.swift|scripts/relaunch.sh|.githooks/*|.gitignore|.gitattributes|.zcodeignore)
      tooling=$((tooling + 1)); case "$path" in *.sh|.githooks/*) want_syntax=1 ;; esac ;;
    macos/Packages/PaxProto/*)
      mark_swift PaxClient "$path"
      [ -n "$want_ui_acceptance" ] || want_ui_acceptance=$path
      case "$path" in *.swift) [ -n "$want_lint_swift" ] || want_lint_swift=$path ;; esac ;;
    macos/Packages/Features/*|macos/Packages/PaxUI/*)
      [ -n "$want_ui_acceptance" ] || want_ui_acceptance=$path
      case "$path" in *.swift) [ -n "$want_lint_swift" ] || want_lint_swift=$path ;; esac ;;
    macos/Packages/*/*)
      pkg=${path#macos/Packages/}; pkg=${pkg%%/*}
      case " ${SWIFT_ORDER[*]} " in
        *" $pkg "*) mark_swift "$pkg" "$path" ;;
        *) for p in "${SWIFT_ORDER[@]}"; do mark_swift "$p" "$path"; done ;;
      esac
      [ -n "$want_ui_acceptance" ] || want_ui_acceptance=$path
      case "$path" in *.swift) [ -n "$want_lint_swift" ] || want_lint_swift=$path ;; esac ;;
    macos/*)
      [ -n "$want_ui_acceptance" ] || want_ui_acceptance=$path
      case "$path" in *.swift) [ -n "$want_lint_swift" ] || want_lint_swift=$path ;; esac ;;
    api/*.proto)
      for p in "${SWIFT_ORDER[@]}"; do mark_swift "$p" "$path"; done
      [ -n "$want_ui_acceptance" ] || want_ui_acceptance=$path
      [ -n "$want_go" ] || want_go=$path
      [ -n "$want_lint_go" ] || want_lint_go=$path ;;
    scripts/test-swift.py)
      want_syntax=1 ;;
    *)
      [ -n "$want_go" ] || want_go=$path
      case "$path" in daemon/*|patches/*|*.go|go.mod|go.sum) [ -n "$want_lint_go" ] || want_lint_go=$path ;; esac
      case "$path" in *.sh|*.py) want_syntax=1 ;; esac ;;
  esac
done < "$tmp"

# 步骤清单：plan 与执行用同一份。
steps=("make lint-size")
[ -z "$want_lint_go" ] || steps+=("make lint-go")
[ -z "$want_lint_swift" ] || steps+=("make lint-swift")
[ -z "$want_syntax" ] || steps+=("scripts/check.sh --syntax")
[ -z "$want_go" ] || steps+=("make test-go")
swift_pkgs=()
for p in "${SWIFT_ORDER[@]}"; do [ -z "$(hit "$p")" ] || swift_pkgs+=("macos/Packages/$p"); done
[ "${#swift_pkgs[@]}" -eq 0 ] || steps+=("make test-swift PKG=\"${swift_pkgs[*]}\"")

echo "check-plan: 变更路径 ${count} 个（文档 ${docs}、工具 ${tooling}）"
for s in "${steps[@]}"; do echo "check-plan: step: $s"; done
if [ -n "$want_go" ]; then echo "check-plan: go 测试因 ${want_go}"; else echo "check-plan: go 测试跳过"; fi
if [ "${#swift_pkgs[@]}" -gt 0 ]; then echo "check-plan: swift 测试 ${swift_pkgs[*]}"; else echo "check-plan: swift 测试跳过"; fi
if [ -n "$want_ui_acceptance" ]; then
  echo "check-plan: 真实验收待执行：因 ${want_ui_acceptance}，按本次修改功能在 make relaunch 后走实际用户路径并留关键截图；PaxUI/Features/App 无自动测试"
fi
[ -z "$plan_only" ] || { echo "check-plan: dry-run，未执行"; exit 0; }

logdir=$(git rev-parse --git-path pax-check-logs)
mkdir -p "$logdir"
log="$logdir/$(date +%Y%m%d-%H%M%S)-$$.log"
echo "check: 日志 $log"

run_steps() {
  for s in "${steps[@]}"; do
    echo "check: >>> $s"
    eval "$s" || return $?
  done
}
set +e
run_steps 2>&1 | tee "$log"
rc=${PIPESTATUS[0]}
set -e
if [ "$rc" -ne 0 ]; then
  echo "check: 失败（exit ${rc}），日志 ${log}"
  echo "check: 失败项："
  grep -aE "Test Case '.*' failed|^✘ Test |^--- FAIL|^FAIL[[:space:]:]|^ERROR:|^error:|timed out|^  - " "$log" | sort -u | head -40 || true
  exit "$rc"
fi
echo "check: 自动检查通过（日志 ${log}）"
[ -z "$want_ui_acceptance" ] || echo "check: 真实用户路径验收仍待单独记录，本脚本未执行验收"
