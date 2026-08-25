package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotBackupScriptPlanInProcess(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "snapshot-backup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	previous := -1
	for _, fragment := range []string{
		`scenery snapshot save --db --storage`,
		`scenery snapshot verify --input`,
		`rclone copyto --checksum`,
		`if (( index > keep )); then`,
		`rm -- "$file"`,
	} {
		index := strings.Index(string(script), fragment)
		if index < 0 || index <= previous {
			t.Fatalf("snapshot backup plan missing or misordered %q", fragment)
		}
		previous = index
	}
}
