//go:build darwin

package host

func convertMaxRSSBytes(maxRSS int64) uint64 {
	if maxRSS <= 0 {
		return 0
	}
	return uint64(maxRSS)
}
