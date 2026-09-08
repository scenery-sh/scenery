package main

import (
	strconv "strconv"
	strings "strings"
)

func atoiPID(value string) int {
	pid, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return pid
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
