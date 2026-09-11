//go:build unix

package host

import (
	"syscall"
)

type processUsage struct {
	UserSeconds                float64
	SystemSeconds              float64
	MaxRSSBytes                uint64
	MinorPageFaults            int64
	MajorPageFaults            int64
	InputBlocks                int64
	OutputBlocks               int64
	VoluntaryContextSwitches   int64
	InvoluntaryContextSwitches int64
}

type diskUsage struct {
	TotalBytes     uint64
	UsedBytes      uint64
	FreeBytes      uint64
	AvailableBytes uint64
}

func readProcessUsage() (processUsage, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return processUsage{}, err
	}
	return processUsage{
		UserSeconds:                timevalSeconds(usage.Utime),
		SystemSeconds:              timevalSeconds(usage.Stime),
		MaxRSSBytes:                maxRSSBytes(usage.Maxrss),
		MinorPageFaults:            usage.Minflt,
		MajorPageFaults:            usage.Majflt,
		InputBlocks:                usage.Inblock,
		OutputBlocks:               usage.Oublock,
		VoluntaryContextSwitches:   usage.Nvcsw,
		InvoluntaryContextSwitches: usage.Nivcsw,
	}, nil
}

func readDiskStats(path string) (diskUsage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return diskUsage{}, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	available := stat.Bavail * uint64(stat.Bsize)
	used := total - free
	return diskUsage{
		TotalBytes:     total,
		UsedBytes:      used,
		FreeBytes:      free,
		AvailableBytes: available,
	}, nil
}

func timevalSeconds(tv syscall.Timeval) float64 {
	return float64(tv.Sec) + float64(tv.Usec)/1e6
}

func maxRSSBytes(maxRSS int64) uint64 {
	return convertMaxRSSBytes(maxRSS)
}
