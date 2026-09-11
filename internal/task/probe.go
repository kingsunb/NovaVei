package task

import (
	"sync"
	"time"

	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/kingsunb/NovaVeil/internal/op"
	"github.com/kingsunb/NovaVeil/internal/relay"
)

// backgroundProbeCheckPeriod 后台探测的固定检查节拍: 任务框架按单一间隔注册定时任务, 而各分组的
// BackgroundProbeIntervalSeconds 可以不同, 故用较短的固定节拍轮询, 在节拍内按各分组自己的到期时间触发,
// 分组配置每次执行时读取最新值, 运行期变更最迟在下个节拍生效。
const backgroundProbeCheckPeriod = 5 * time.Second

var (
	backgroundProbeMu    sync.Mutex
	backgroundProbeDueAt = make(map[int]int64) // 分组 ID 到下次允许触发的 Unix 毫秒时间。
)

// BackgroundProbe 遍历全部分组, 对启用后台探测的故障转移分组检查是否到达各自的探测间隔,
// 到期即把冷却已到期且未被占用的成员交给 relay 的统一异步探测入口。无启用分组时本次执行为快速空转。
func BackgroundProbe() {
	now := time.Now().UnixMilli()
	backgroundProbeMu.Lock()
	var due []model.Group
	for _, group := range op.GroupList() {
		if group.Mode != model.GroupModeFailover || !group.RelayConfig.BackgroundProbeEnabled {
			delete(backgroundProbeDueAt, group.ID)
			continue
		}
		if now < backgroundProbeDueAt[group.ID] {
			continue
		}
		intervalSeconds := max(group.RelayConfig.BackgroundProbeIntervalSeconds, 1)
		backgroundProbeDueAt[group.ID] = now + int64(intervalSeconds)*1000
		due = append(due, group)
	}
	backgroundProbeMu.Unlock()

	for _, group := range due {
		relay.ProbeExpiredItems(group)
	}
}
