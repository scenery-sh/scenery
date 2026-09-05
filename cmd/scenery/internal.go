package main

import "fmt"

func internalCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery internal <command> ")
	}
	switch args[0] {
	default:
		return fmt.Errorf("unknown internal command %q", args[0])
	}
}
