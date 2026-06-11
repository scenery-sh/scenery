package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

type testEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
}

type elapsedRecord struct {
	Name    string
	Elapsed float64
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var input io.Reader = os.Stdin
	if len(args) > 1 {
		return fmt.Errorf("usage: go run ./scripts/slowtests.go [go-test-json-file]")
	}
	if len(args) == 1 {
		file, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer file.Close()
		input = file
	}

	var packages []elapsedRecord
	var tests []elapsedRecord
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var event testEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return err
		}
		if event.Action != "pass" && event.Action != "fail" {
			continue
		}
		if event.Elapsed <= 0 {
			continue
		}
		if event.Test == "" {
			packages = append(packages, elapsedRecord{Name: event.Package, Elapsed: event.Elapsed})
			continue
		}
		tests = append(tests, elapsedRecord{Name: event.Package + " " + event.Test, Elapsed: event.Elapsed})
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	sort.Slice(packages, func(i, j int) bool {
		return packages[i].Elapsed > packages[j].Elapsed
	})
	sort.Slice(tests, func(i, j int) bool {
		return tests[i].Elapsed > tests[j].Elapsed
	})

	printRecords("slow packages", packages)
	fmt.Println()
	printRecords("slow tests", tests)
	return nil
}

func printRecords(title string, records []elapsedRecord) {
	fmt.Println(title)
	fmt.Println("elapsed  name")
	limit := min(len(records), 20)
	for _, record := range records[:limit] {
		fmt.Printf("%7.3fs  %s\n", record.Elapsed, record.Name)
	}
}
