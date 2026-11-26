package generate

import (
	"strings"
)

func assistantNameFromCanonicalAddress(address string) string {
	address = strings.TrimSuffix(strings.TrimSpace(address), "/")
	if index := strings.LastIndexByte(address, '/'); index >= 0 {
		address = address[index+1:]
	}
	return address
}
