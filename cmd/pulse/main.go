package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "run":
		return runCommand(args[1:])
	case "build":
		return buildCommand(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usageError() error {
	return fmt.Errorf("usage:\n  pulse run [--port <n>] [--listen <addr>] [--app-root <path>] [-v|--verbose]\n  pulse build [--app-root <path>] [-o <path>]")
}

func runCommand(args []string) error {
	listen := ""
	port := 4000
	verbose := false
	appRoot := ""
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
		case "--verbose", "-v":
			verbose = true
		case "--app-root":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing value for --app-root")
			}
			appRoot = args[i]
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}

	addr := resolveListenAddr(listen, port)
	return runWithWatch(addr, verbose, appRoot)
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

func resolveAppRoot(start string) (string, error) {
	if start == "" {
		return ".", nil
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	return abs, nil
}
