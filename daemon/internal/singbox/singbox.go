package singbox

import (
	"github.com/sagernet/sing-box/constant"
)

// PinnedModuleVersion 是 go.mod 里锁定的 sing-box module 版本。启动自检用它记录内核版本。
//
// 不用 constant.Version：那个值是 sing-box 官方构建时用 ldflags 注入的，作为依赖被引入时为空。
const PinnedModuleVersion = "v1.14.3"

// KernelVersion 返回运行时内核自报的版本（依赖方式引入时通常为空，保留供诊断对照）。
func KernelVersion() string {
	return constant.Version
}
