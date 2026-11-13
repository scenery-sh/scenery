package main

func systemCommand(args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: scenery system agent|edge|toolchain")
	}
	if err := flagBeforeWord(args, "the system subcommand"); err != nil {
		return err
	}
	switch args[0] {
	case "agent":
		return agentCommand(args[1:])
	case "edge":
		return edgeCommand(args[1:])
	case "toolchain":
		return toolchainCommand(args[1:])
	default:
		return usageErrorf("unknown system command %q", args[0])
	}
}
