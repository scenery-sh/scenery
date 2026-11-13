package main

import (
	"context"
	"io"
	"os"
)

func checkCommand(args []string) error {
	return runSceneryCheck(context.Background(), os.Stdout, args)
}

func runSceneryCheck(_ context.Context, stdout io.Writer, args []string) error {
	return runContractCheck(stdout, args)
}
