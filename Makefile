# Pax Connect 构建入口（架构 §17、AGENTS.md「常用命令」）
#
#   make gen      生成 proto 两侧代码（Go + Swift）
#   make daemon   编译 pax-connectd 与 pax-core（arm64）
#   make app      构建 AppKit App（需要 macos/PaxConnect.xcodeproj，见 macos/README.md）
#   make lint     lint-size + lint-go + lint-swift 的合集
#   make lint-size / lint-go / lint-swift   check-size（--changed）/ gofmt+go vet+golangci-lint / swift-format，可单跑
#   make test     全量（Go + 保留的 Swift 测试包），必须带 FULL_REASON="工单/用户确认的理由"，否则拒绝；
#                 带理由时时间戳+理由+结果追加到 $(git rev-parse --git-path pax-full-runs.log)，输出另存 pax-check-logs/
#   make check    按变更路径（相对 HEAD，含未跟踪）选最小检查集：文档只跑 lint-size；Swift 按保留测试包依赖选下游；
#                 Features/App 改动走真实路径验收；Go 改动才跑 Go 测试；lint 只查涉及的语言。CHECK_PLAN=1 只打印计划（见 scripts/check.sh）
#   make test-go    [PKG=./internal/controller] [RUN=TestX]  指定 PKG/RUN 时只跑该范围（integration tag 同全量）
#   make test-swift [PKG=macos/Packages/PaxCore] [FILTER=Suite/test]  只跑指定 package / 过滤器
#                 两者共用 TEST_TIMEOUT（默认 10m，如 TEST_TIMEOUT=20m）：Go 用 -timeout，
#                 Swift 由 scripts/test-swift.py 超时终止并打印未结束的用例；均为单个测试进程的上限
#   make relaunch 退出旧实例 → 删除已核验身份的旧 Debug bundle → make app → 启动新构建（scripts/relaunch.sh）
#   make ai-usage-rebuild [ARGS="-dry-run -dir DIR -home DIR"]
#                 重建 ai_usage.db（先退出 App；备份 → 写入切换记录 → 重扫日志 → 对账 → 替换）。
#                 -dry-run 只构建并输出报告，不替换、不写任何文件（daemon/cmd/pax-ai-usage-rebuild）
#   make hooks    显示仓库 hook 策略：不启用提交前检查
#   make clean    清理构建产物
#   提交：scripts/commit.sh "<type(scope): subject>" [body…]（标题/暂存区校验 + git commit；检查手动运行）
SHELL := /bin/bash
.SHELLFLAGS := -e -o pipefail -c
VERSION ?= 0.1.0-dev
export GOPROXY ?= https://goproxy.cn,direct
export PATH := $(CURDIR)/.tools/bin:$(PATH)

SWIFT_PACKAGES := $(wildcard macos/Packages/*)
SWIFT_TEST_PACKAGES := $(filter-out macos/Packages/Features macos/Packages/PaxProto macos/Packages/PaxUI,$(SWIFT_PACKAGES))
GO_TAGS := with_quic,with_gvisor,with_utls,with_clash_api,pax_tun_guard
TEST_TIMEOUT ?= 10m
PKG ?=
RUN ?=
FILTER ?=
FULL_REASON ?=

.PHONY: all gen daemon app lint lint-size lint-go lint-swift test test-go test-swift check relaunch ai-usage-rebuild hooks clean

all: daemon

gen:
	./scripts/gen-proto.sh

daemon:
	VERSION=$(VERSION) ./scripts/build-daemon.sh

app: daemon
	@test -e macos/PaxConnect.xcodeproj || { \
		echo "error: 缺少 macos/PaxConnect.xcodeproj"; \
		echo "       请在 Xcode 里按 macos/README.md 的「Xcode 收尾清单」建好工程后再跑 make app"; \
		exit 1; }
	./scripts/build-app.sh

# 门禁：工具缺失或检查失败均返回非零。
lint: lint-size lint-go lint-swift

lint-size:
	./scripts/check-size.sh --changed

lint-go:
	@command -v golangci-lint >/dev/null || { echo "error: 缺少 golangci-lint"; exit 1; }
	@test -z "$$(gofmt -l daemon patches/sing-tun patches/sing-box)" || { gofmt -l daemon patches/sing-tun patches/sing-box; exit 1; }
	cd daemon && ../scripts/go.sh vet -tags with_quic,with_gvisor,with_utls,with_clash_api,pax_tun_guard ./...
	cd daemon && golangci-lint run --build-tags $(GO_TAGS) ./...

lint-swift:
	@xcrun --find swift-format >/dev/null
	@find macos -name '*.swift' -not -path '*/.build/*' -not -path '*/Generated/*' -print0 | xargs -0 xcrun swift-format lint --strict

# 全量：先向用户说明并获确认，再带 FULL_REASON 运行；定向用 test-go PKG=/RUN=、test-swift PKG=/FILTER=，不受此限。
test:
ifeq ($(strip $(FULL_REASON)),)
	@echo "error: make test 是全量测试，需要 FULL_REASON。" >&2
	@echo "       全量需先向用户说明命中哪条、触发改动、为何受影响范围不够、预计耗时，经确认后带 FULL_REASON 运行：" >&2
	@echo "       make test FULL_REASON=\"<命中条款/触发改动/用户确认>\"" >&2
	@echo "       定向用法：make test-go PKG=… RUN=…；make test-swift PKG=… FILTER=…；按变更选测：make check" >&2
	@exit 1
else
	@dir="$$(git rev-parse --git-path pax-check-logs)"; mkdir -p "$$dir"; ts="$$(date +%Y%m%d-%H%M%S)"; \
	 log="$$dir/$$ts-full-test.log"; echo "full test: 日志 $$log"; \
	 set +e; $(MAKE) test-go test-swift 2>&1 | tee "$$log"; rc=$${PIPESTATUS[0]}; set -e; \
	 if [ "$$rc" -eq 0 ]; then r=pass; else r=fail; fi; \
	 printf '%s\t%s\t%s\t%s\n' "$$(date '+%Y-%m-%d %H:%M:%S')" "$(FULL_REASON)" "$$r" "$$log" >> "$$(git rev-parse --git-path pax-full-runs.log)"; \
	 if [ "$$r" != pass ]; then echo "full test: 失败项："; grep -aE "Test Case '.*' failed|^✘ Test |^--- FAIL|^FAIL[[:space:]:]|^ERROR:|^error:|timed out|^  - " "$$log" | sort -u | head -40; fi; \
	 [ "$$r" = pass ]
endif

test-go:
ifneq ($(strip $(PKG)$(RUN)),)
	cd daemon && ../scripts/go.sh test -tags integration,$(GO_TAGS) -count=1 -timeout $(TEST_TIMEOUT) $(if $(value RUN),-v -run '$(value RUN)') $(or $(PKG),./...)
else
	@echo "--- go test ---"
	cd daemon && ../scripts/go.sh test -tags integration,$(GO_TAGS) -timeout $(TEST_TIMEOUT) ./...
	cd daemon && ../scripts/go.sh test -timeout $(TEST_TIMEOUT) github.com/sagernet/sing-tun -run '^TestPax' -count=1
	cd daemon && ../scripts/go.sh test -timeout $(TEST_TIMEOUT) github.com/sagernet/sing-box/common/settings -run '^TestPaxProxy' -count=1
	cd daemon && ../scripts/go.sh test -timeout $(TEST_TIMEOUT) github.com/sagernet/sing-box/dns -run '^TestPaxDNS' -count=1
	python3 scripts/test-migrate-root-data.py
	python3 -B scripts/test-quiesce-build-app.py
	python3 -B tests/proxy-session/test_probe.py
endif

test-swift:
	@echo "--- swift test ---"
	python3 scripts/test-swift.py --timeout $(TEST_TIMEOUT) $(if $(value FILTER),--filter '$(value FILTER)') $(or $(PKG),$(SWIFT_TEST_PACKAGES))

check:
	./scripts/check.sh
	@[ -n "$$CHECK_PLAN" ] || ./scripts/worktree-hash.sh > "$$(git rev-parse --git-path pax-check-ok)"

ai-usage-rebuild:
	cd daemon && ../scripts/go.sh run ./cmd/pax-ai-usage-rebuild $(ARGS)

relaunch:
	./scripts/relaunch.sh

hooks:
	@echo "仓库不启用提交前检查；请手动运行 make lint / check / test"

clean:
	rm -rf build macos/.build
	@for package in $(SWIFT_PACKAGES); do rm -rf "$$package/.build"; done
	@echo "已清理 build/、macos/.build/ 与各 package 的 .build/"
