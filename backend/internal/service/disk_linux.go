//go:build linux

package service

import "syscall"

// getRootDiskUsagePercent 获取根分区使用率(0-100)
// Linux: 通过 syscall.Statfs 读取
func getRootDiskUsagePercent() float64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return 0
	}
	total := stat.Blocks * uint64(stat.Bsize)
	if total == 0 {
		return 0
	}
	used := (stat.Blocks - stat.Bavail) * uint64(stat.Bsize)
	return float64(used) * 100 / float64(total)
}

// readDiskUsage 通过 statfs 读取指定路径所在文件系统的容量与剩余字节数
func readDiskUsage(path string) (total, free uint64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	total = stat.Blocks * uint64(stat.Bsize)
	free = stat.Bavail * uint64(stat.Bsize)
	return total, free, nil
}
