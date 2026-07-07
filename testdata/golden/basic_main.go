package main

import (
	_ "example.com/basicapp/service"
	"fmt"
	"os"
	sceneryruntime "scenery.sh/runtime"
)

func main() {
	if err := sceneryruntime.Main(sceneryruntime.AppConfig{Name: "basicapp", ListenAddr: sceneryruntime.ListenAddrFromEnv()}); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "scenery: %v\n", err)
		os.Exit(1)
	}
}
