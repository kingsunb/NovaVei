//go:build linux || darwin

package op

import (
	"golang.org/x/sys/unix"

	"github.com/kingsunb/NovaVei/internal/model"
)

// diskHasRoomFor 检查路径所在磁盘的剩余空间占比是否高于水位线;
// 统计失败时按有空间处理, 不阻断正常写入。
func diskHasRoomFor(path string) bool {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return true
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	if total == 0 {
		return true
	}
	return free*100 > total*uint64(model.ConversationDiskFreeMinPercent)
}
