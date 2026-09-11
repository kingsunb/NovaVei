//go:build !linux && !darwin

package op

// diskHasRoomFor 非 unix 平台暂不支持水位检测, 恒按有空间处理。
func diskHasRoomFor(path string) bool {
	return true
}
