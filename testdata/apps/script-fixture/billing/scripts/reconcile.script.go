//go:build ignore

package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Printf("reconcile args=%s app=%s\n", strings.Join(os.Args[1:], ","), os.Getenv("ONLAVA_APP_ID"))
}
