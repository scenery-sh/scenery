//go:build !unix

package host

import "errors"

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
	return processUsage{}, errors.New("process usage unavailable on this platform")
}

func readDiskStats(string) (diskUsage, error) {
	return diskUsage{}, errors.New("disk stats unavailable on this platform")
}
