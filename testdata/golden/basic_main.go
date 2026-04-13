package main

import (
	_ "example.com/basicapp/service"
	pulseruntime "pulse.dev/runtime"
)

func main() {
	if err := pulseruntime.Main(pulseruntime.AppConfig{Name: "basicapp", ListenAddr: pulseruntime.ListenAddrFromEnv()}); err != nil {
		panic(err)
	}
}
