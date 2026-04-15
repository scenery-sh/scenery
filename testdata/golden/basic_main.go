package main

import (
	_ "example.com/basicapp/service"
	"fmt"
	"os"
	pulseruntime "pulse.dev/runtime"
)

func main() {
	if err := pulseruntime.Main(pulseruntime.AppConfig{Name: "basicapp", Workspace: "basic", ListenAddr: pulseruntime.ListenAddrFromEnv()}); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "pulse: %v\n", err)
		os.Exit(1)
	}
}
