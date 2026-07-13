package main

import (
	"io"
)

type adminOptions struct {
	Domain  string
	Action  string
	AppRoot string
	JSON    bool
}

type adminResponse struct {
	cliPayloadIdentity
	OK       bool           `json:"ok"`
	Command  string         `json:"command"`
	App      adminAppRef    `json:"app"`
	Warnings []string       `json:"warnings,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

type adminAppRef struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

func writeAdminJSON(w io.Writer, payload adminResponse) error {
	return writeCLIJSON(w, payload)
}
