//go:build !linux

package service

// getRootDiskUsagePercent 获取根分区使用率(0-100)
// 非 Linux 平台(如 Windows 开发环境)返回 0, 生产环境为 Linux
func getRootDiskUsagePercent() float64 {
	return 0
}

// readDiskUsage 非 Linux 平台返回 0(用于编译兼容, 生产环境为 Linux)
func readDiskUsage(path string) (total, free uint64, err error) {
	return 0, 0, nil
}
