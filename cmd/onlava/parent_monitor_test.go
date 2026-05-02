package main

import "testing"

func TestParentMonitorShouldCancel(t *testing.T) {
	tests := []struct {
		name    string
		initial int
		current int
		want    bool
	}{
		{name: "same parent", initial: 123, current: 123, want: false},
		{name: "reparented to pid1", initial: 123, current: 1, want: true},
		{name: "reparented elsewhere", initial: 123, current: 456, want: true},
		{name: "initial pid1 ignored", initial: 1, current: 1, want: false},
		{name: "invalid current ignored", initial: 123, current: 0, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parentMonitorShouldCancel(tt.initial, tt.current); got != tt.want {
				t.Fatalf("parentMonitorShouldCancel(%d, %d) = %v, want %v", tt.initial, tt.current, got, tt.want)
			}
		})
	}
}
