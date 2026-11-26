package main

type workerAssistantProcess struct {
	PID         int    `json:"assistant_pid,omitempty"`
	RuntimePath string `json:"assistant_runtime_path,omitempty"`
	KeyPath     string `json:"assistant_key_path,omitempty"`
}
