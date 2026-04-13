package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"

	"pulse.dev/internal/app"
	"pulse.dev/internal/build"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pulse run [--port <n>] [--listen <addr>]")
	}
	switch args[0] {
	case "run":
		return runCommand(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runCommand(args []string) error {
	listen := ""
	port := 4000
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port", "-p":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing value for --port")
			}
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("invalid port %q", args[i])
			}
			port = value
		case "--listen":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing value for --listen")
			}
			listen = args[i]
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}

	addr := resolveListenAddr(listen, port)
	root, cfg, err := app.DiscoverRoot(".")
	if err != nil {
		return err
	}
	result, err := build.App(root, cfg.Name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(result.Dir)

	cmd := exec.Command(result.Binary)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PULSE_LISTEN_ADDR="+addr)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func resolveListenAddr(listen string, port int) string {
	if listen == "" {
		return fmt.Sprintf("127.0.0.1:%d", port)
	}
	if _, _, err := net.SplitHostPort(listen); err == nil {
		return listen
	}
	return net.JoinHostPort(listen, strconv.Itoa(port))
}
