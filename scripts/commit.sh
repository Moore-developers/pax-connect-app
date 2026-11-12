#!/usr/bin/env bash
# 提交入口：scripts/commit.sh "<type(scope): subject>" [body 段落…]
#   1. 校验提交标题格式（仓库历史一贯采用的 Conventional Commits：type(scope): subject）
#        type ∈ feat fix docs refactor style test chore perf build ci revert
#        scope 可选，小写字母/数字/._/- ；标题 ≤ 72 字符；冒号后一个空格；不以句号结尾
#      每个后续参数作为一个正文段落（git commit -m 多次）。
#   2. 暂存区为空则拒绝（不自动 git add，避免误提交）
#   3. git commit；测试、lint 和字节检查由调用者手动运行，不在提交前自动执行。
#      COMMIT_DRY_RUN=1：只校验并打印，不提交（暂存区为空也不报错）。
# 分支合并前的评审、分支命名等流程规则见 docs/code-standards.md §5.4（短分支、快进合入 main）。
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

subject=${1:-}
[ -n "$subject" ] || { echo '用法: scripts/commit.sh "<type(scope): subject>" [body…]' >&2; exit 2; }
shift

pattern='^(feat|fix|docs|refactor|style|test|chore|perf|build|ci|revert)(\([a-z0-9._/-]+\))?!?: [^ ].*[^.]$'
if ! [[ $subject =~ $pattern ]] || [ "${#subject}" -gt 72 ]; then
  echo "error: 标题不符合 '<type>(<scope>): <subject>'（≤72 字符、末尾无句号）：$subject" >&2
  exit 2
fi

dry=${COMMIT_DRY_RUN:-}
if git diff --cached --quiet; then
  [ -n "$dry" ] || { echo "error: 暂存区为空，先 git add 要提交的文件" >&2; exit 1; }
  echo "commit(dry-run): 暂存区为空（真实提交会拒绝）"
fi

if [ -n "$dry" ]; then
  echo "commit(dry-run): 标题校验通过，不提交；检查由调用者手动运行"
  exit 0
fi

message=(-m "$subject")
for paragraph in "$@"; do message+=(-m "$paragraph"); done
git commit "${message[@]}"
