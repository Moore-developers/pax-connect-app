#!/usr/bin/env bash
# UI 验收截图：按 PID 定位 PaxConnect 窗口 → 调尺寸 → 点击若干控件 → 截窗口图，一条命令。
# 用法：scripts/ui-shot.sh [--pid N] [--size WxH] [--click 标识]... [--out 路径] [--depth N]
#   --pid    默认按 bundle id dev.moore.PaxConnect 查找
#   --size   默认 1280x860（固定值，保证截图可比）
#   --click  可重复，按顺序执行；先按 AXIdentifier，再按 AXTitle/AXDescription/AXValue 精确匹配；
#            在窗口子树内广度优先搜索（--depth，默认 14）；找不到时报错并列出最深层候选（≤20）
#   --out    默认 .scratch/ui-shot/<时间戳>.png；成功后打印路径
# 例：scripts/ui-shot.sh --click 网络概览 --click Token --out .scratch/ui-shot/token.png
# 需要终端具备「辅助功能」与「屏幕录制」权限。首次运行会编译 swift 到 build/ui-shot。
set -euo pipefail
cd "$(dirname "$0")/.."
bin=build/ui-shot
if [[ ! -x $bin || scripts/ui-shot.swift -nt $bin ]]; then
  mkdir -p build
  swiftc -O scripts/ui-shot.swift -o "$bin"
fi
exec "$bin" "$@"
