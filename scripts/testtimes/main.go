package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type goTestEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
}

type testTiming struct {
	Seconds float64
	Status  string
	Package string
	Test    string
}

const defaultPackageParallelism = "4"
const shardedCmdSceneryPackage = "scenery.sh/cmd/scenery"

var shardedCmdSceneryDefaultRegexes = []string{
	"^Test[A-E].*",
	"^Test[F-L].*",
	"^Test[M-O].*",
	"^TestP.*",
	"^TestR.*",
	"^Test([^A-Z]|[QST-Z]).*",
}

func main() {
	exitCode, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

func run(args []string) (int, error) {
	started := time.Now()
	exitCode, rows, err := runTests(args)
	printTable(rows, time.Since(started))
	return exitCode, err
}

func runTests(args []string) (int, []testTiming, error) {
	if useDefaultShards(args) {
		return runDefaultShards()
	}
	goArgs := buildGoTestArgs(args)
	return runAndCollect("", "go", goArgs...)
}

func runAndCollect(dir, name string, args ...string) (int, []testTiming, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 1, nil, err
	}
	if err := cmd.Start(); err != nil {
		return 1, nil, err
	}

	var rows []testTiming
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var event goTestEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Test == "" {
			continue
		}
		switch event.Action {
		case "pass", "fail", "skip":
			rows = append(rows, testTiming{
				Seconds: event.Elapsed,
				Status:  event.Action,
				Package: event.Package,
				Test:    event.Test,
			})
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()

	exitCode := 0
	if waitErr != nil {
		exitCode = commandExitCode(waitErr)
	}
	if scanErr != nil {
		return exitCode, rows, scanErr
	}
	if waitErr != nil {
		return exitCode, rows, waitErr
	}
	return 0, rows, nil
}

func useDefaultShards(args []string) bool {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	return len(args) == 0
}

func runDefaultShards() (int, []testTiming, error) {
	type result struct {
		rows     []testTiming
		exitCode int
		err      error
	}

	results := make(chan result, len(shardedCmdSceneryDefaultRegexes)+1)
	var wg sync.WaitGroup
	run := func(args []string) {
		defer wg.Done()
		exitCode, rows, err := runAndCollect("", "go", args...)
		results <- result{rows: rows, exitCode: exitCode, err: err}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		packages, err := listPackages()
		if err != nil {
			results <- result{exitCode: 1, err: err}
			return
		}
		otherPackages := make([]string, 0, len(packages))
		for _, pkg := range packages {
			if pkg != shardedCmdSceneryPackage {
				otherPackages = append(otherPackages, pkg)
			}
		}
		if len(otherPackages) == 0 {
			results <- result{}
			return
		}
		goArgs := append([]string{"test", "-json", "-p=" + defaultPackageParallelism}, otherPackages...)
		exitCode, rows, err := runAndCollect("", "go", goArgs...)
		results <- result{rows: rows, exitCode: exitCode, err: err}
	}()

	for _, regex := range shardedCmdSceneryDefaultRegexes {
		wg.Add(1)
		go run([]string{"test", "-json", "-run", regex, "./cmd/scenery"})
	}

	wg.Wait()
	close(results)

	var rows []testTiming
	exitCode := 0
	var err error
	for result := range results {
		rows = append(rows, result.rows...)
		if result.exitCode != 0 && exitCode == 0 {
			exitCode = result.exitCode
		}
		if result.err != nil && err == nil {
			err = result.err
		}
	}
	return exitCode, rows, err
}

func listPackages() ([]string, error) {
	cmd := exec.Command("go", "list", "./...")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(out)), nil
}

func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func buildGoTestArgs(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}

	goArgs := []string{"test", "-json"}
	if !hasPackageParallelism(args) {
		goArgs = append(goArgs, "-p="+defaultPackageParallelism)
	}
	goArgs = append(goArgs, args...)
	if !hasPackagePattern(args) {
		goArgs = append(goArgs, "./...")
	}
	return goArgs
}

func hasPackageParallelism(args []string) bool {
	for _, arg := range args {
		switch {
		case arg == "-p":
			return true
		case arg == "--":
			return false
		case len(arg) > len("-p=") && arg[:len("-p=")] == "-p=":
			return true
		}
	}
	return false
}

func hasPackagePattern(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return false
		}
		if arg == "-args" || arg == "--args" {
			return false
		}
		if strings.HasPrefix(arg, "-") {
			if flagConsumesValue(arg) && !strings.Contains(arg, "=") {
				i++
			}
			continue
		}
		return true
	}
	return false
}

func flagConsumesValue(arg string) bool {
	name := strings.TrimLeft(arg, "-")
	if i := strings.Index(name, "="); i >= 0 {
		name = name[:i]
	}
	switch name {
	case "C", "bench", "benchtime", "blockprofile", "blockprofilerate", "count", "covermode",
		"coverpkg", "coverprofile", "cpu", "cpuprofile", "exec", "fuzz", "fuzztime", "fuzzminimizetime",
		"list", "memprofile", "memprofilerate", "mutexprofile", "mutexprofilefraction",
		"o", "outputdir", "p", "parallel", "run", "shuffle", "skip", "timeout", "trace", "vet":
		return true
	default:
		return false
	}
}

func printTable(rows []testTiming, actualElapsed time.Duration) {
	var totalSeconds float64
	for _, row := range rows {
		totalSeconds += row.Seconds
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Seconds != rows[j].Seconds {
			return rows[i].Seconds < rows[j].Seconds
		}
		if rows[i].Package != rows[j].Package {
			return rows[i].Package < rows[j].Package
		}
		return rows[i].Test < rows[j].Test
	})

	fmt.Printf("%8s  %-7s  %-60s  %s\n", "seconds", "status", "package", "test")
	fmt.Printf("%8s  %-7s  %-60s  %s\n", "-------", "------", "-------", "----")
	for _, row := range rows {
		fmt.Printf("%8.2f  %-7s  %-60s  %s\n", row.Seconds, row.Status, row.Package, row.Test)
	}
	fmt.Printf("\n%d tests\n", len(rows))
	fmt.Printf("total test time: %.2fs\n", totalSeconds)
	fmt.Printf("total actual time: %.2fs\n", actualElapsed.Seconds())
}

func commandExitCode(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
	}
	return 1
}
