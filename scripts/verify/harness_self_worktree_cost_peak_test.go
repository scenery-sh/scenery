package main

import "testing"

func TestWorktreeCostPeakSample(t *testing.T) {
	output := []byte("1 0 100 unrelated\n2 1 200 scenery --app-root /owned/worktree\n3 2 300 compiler\n4 3 400 linker\n5 1 500 victoria --storageDataPath=/private/worktree/data\n6 1 600 other-user-process\n")
	rss, processes, err := worktreeCostPeakSample(output, []string{"/owned/worktree", "/private/worktree"})
	if err != nil || rss != 1400 || processes != 4 {
		t.Fatalf("owned sample = %d KiB, %d processes, %v", rss, processes, err)
	}
	if _, _, err := worktreeCostPeakSample([]byte("2 1 invalid scenery /owned/worktree\n"), []string{"/owned/worktree"}); err == nil {
		t.Fatal("malformed sample was accepted")
	}
}
