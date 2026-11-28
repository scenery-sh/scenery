package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
)

func runHarnessGofmtStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "gofmt",
		Command: []string{"gofmt", "-l", "."},
	}
	gofmtPath, err := exec.LookPath("gofmt")
	if err != nil {
		step.DurationMS = time.Since(started).Milliseconds()
		step.Error = "gofmt not found in PATH"
		step.Diagnostics = []checkDiagnostic{{
			Stage:           step.Name,
			Severity:        "error",
			Message:         step.Error,
			SuggestedAction: installSuggestion("go"),
		}}
		return step
	}
	output, err := runHarnessGit(ctx, repoRoot, "ls-files", "--", "*.go")
	if err != nil {
		step.DurationMS = time.Since(started).Milliseconds()
		step.Error = "list tracked Go files: " + err.Error()
		step.Diagnostics = []checkDiagnostic{{
			Stage:           step.Name,
			Severity:        "error",
			Message:         step.Error,
			SuggestedAction: "Run `git ls-files -- '*.go'` from the repo root and inspect repository state.",
		}}
		return step
	}
	var files []string
	for _, rel := range splitCommandLines(output) {
		if pathExists(filepath.Join(repoRoot, filepath.FromSlash(rel))) {
			files = append(files, rel)
		}
	}
	if len(files) == 0 {
		step.OK = true
		step.DurationMS = time.Since(started).Milliseconds()
		step.Summary = map[string]any{"checked_files": 0, "unformatted_files": 0}
		return step
	}
	unformatted, err := runGofmtList(ctx, gofmtPath, repoRoot, files)
	step.DurationMS = time.Since(started).Milliseconds()
	step.Summary = map[string]any{
		"checked_files":     len(files),
		"unformatted_files": len(unformatted),
	}
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		step.Diagnostics = []checkDiagnostic{{
			Stage:           step.Name,
			Severity:        "error",
			Message:         step.Error,
			SuggestedAction: "Run `gofmt -l .` in the repository root and fix any syntax or formatting errors.",
		}}
		return step
	}
	for _, rel := range unformatted {
		rel = filepath.ToSlash(rel)
		step.Diagnostics = append(step.Diagnostics, checkDiagnostic{
			Stage:           step.Name,
			Severity:        "error",
			File:            rel,
			Message:         "Go file is not gofmt-formatted",
			SuggestedAction: "Run `gofmt -w " + rel + "`.",
		})
	}
	step.OK = len(unformatted) == 0
	if !step.OK {
		step.Error = fmt.Sprintf("%d Go file(s) are not gofmt-formatted", len(unformatted))
	}
	return step
}

func runGofmtList(ctx context.Context, gofmtPath, repoRoot string, files []string) ([]string, error) {
	const batchSize = 256
	var unformatted []string
	for start := 0; start < len(files); start += batchSize {
		end := min(start+batchSize, len(files))
		args := append([]string{"-l"}, files[start:end]...)
		cmd := commandTreeContext(ctx, gofmtPath, args...)
		cmd.Dir = repoRoot
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("gofmt -l: %w: %s", err, tailString(firstNonEmpty(stderr.String(), stdout.String()), 4096))
		}
		unformatted = append(unformatted, splitCommandLines(stdout.String())...)
	}
	sort.Strings(unformatted)
	return unformatted, nil
}

// runHarnessGoLintStep runs golangci-lint (which includes govet via .golangci.yml)
// in default/race/release lanes, falling back to standalone `go vet ./...` with
// a warning when golangci-lint is unavailable in PATH.
func runHarnessGoLintStep(ctx context.Context, repoRoot string, artifactCtx harnessArtifactContext) harnessStep {
	if _, err := exec.LookPath("golangci-lint"); err == nil {
		return runHarnessExecStep(ctx, repoRoot, "golangci-lint", []string{"golangci-lint", "run", "./..."}, artifactCtx)
	}
	step := runHarnessExecStep(ctx, repoRoot, "go vet", []string{"go", "vet", "./..."}, artifactCtx)
	step.Diagnostics = append(step.Diagnostics, checkDiagnostic{
		Stage:           step.Name,
		Severity:        "warning",
		Message:         "golangci-lint was not found in PATH; fell back to `go vet ./...`",
		SuggestedAction: installSuggestion("golangci-lint"),
	})
	return step
}

type docSCNBlock struct {
	DocRel string
	Line   int
	Body   string
}

func runHarnessDocSCNFormatStep(ctx context.Context, repoRoot, binaryPath string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "scn doc format",
		Command: harnessStaticCheckCommand(repoRoot),
	}
	docs := harnessDocSCNPaths(repoRoot)
	var blocks []docSCNBlock
	skipped := 0
	var diagnostics []checkDiagnostic
	for _, rel := range docs {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           step.Name,
				Severity:        "error",
				File:            rel,
				Message:         err.Error(),
				SuggestedAction: "Ensure the documentation file is readable, then rerun `go run ./scripts/verify --quick --summary --write`.",
			})
			continue
		}
		found, skippedInDoc := extractDocSCNBlocks(rel, string(data))
		blocks = append(blocks, found...)
		skipped += skippedInDoc
	}
	if len(blocks) > 0 && !hasErrorDiagnostics(diagnostics) {
		fmtDiagnostics, err := checkDocSCNBlocksWithBinary(ctx, binaryPath, blocks)
		if err != nil {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           step.Name,
				Severity:        "error",
				Message:         err.Error(),
				SuggestedAction: "Fix the `.scn` doc-block format check error, then rerun `go run ./scripts/verify --quick --summary --write`.",
			})
		} else {
			diagnostics = append(diagnostics, fmtDiagnostics...)
		}
	}
	step.DurationMS = time.Since(started).Milliseconds()
	step.Diagnostics = diagnostics
	step.Summary = map[string]any{
		"docs_checked":   len(docs),
		"blocks_checked": len(blocks),
		"blocks_skipped": skipped,
	}
	step.OK = !hasErrorDiagnostics(diagnostics)
	if !step.OK {
		step.Error = "documentation .scn blocks require formatting"
	}
	return step
}

func harnessDocSCNPaths(repoRoot string) []string {
	docs := []string{
		"README.md",
		"docs/spec/SPEC.md",
		"SKILL.md",
	}
	matches, _ := filepath.Glob(filepath.Join(repoRoot, ".claude", "skills", "*", "SKILL.md"))
	sort.Strings(matches)
	for _, match := range matches {
		if rel, err := filepath.Rel(repoRoot, match); err == nil {
			docs = append(docs, filepath.ToSlash(rel))
		}
	}
	return docs
}

func extractDocSCNBlocks(docRel, text string) ([]docSCNBlock, int) {
	var blocks []docSCNBlock
	skipped := 0
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	inFence := false
	fenceChar := byte(0)
	fenceLen := 0
	isSCN := false
	skipBlock := false
	startLine := 0
	var body []string

	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inFence {
			char, count, info, ok := parseMarkdownFenceOpen(trimmed)
			if !ok {
				continue
			}
			inFence = true
			fenceChar = char
			fenceLen = count
			isSCN, skipBlock = classifySCNFenceInfo(info)
			startLine = index + 2
			body = body[:0]
			continue
		}
		if isMarkdownFenceClose(trimmed, fenceChar, fenceLen) {
			if isSCN {
				content := strings.Join(body, "\n")
				if skipBlock || strings.TrimSpace(content) == "" || docSCNBlockLooksTruncated(content) {
					skipped++
				} else {
					blocks = append(blocks, docSCNBlock{
						DocRel: docRel,
						Line:   startLine,
						Body:   content + "\n",
					})
				}
			}
			inFence = false
			isSCN = false
			skipBlock = false
			body = body[:0]
			continue
		}
		if isSCN {
			body = append(body, line)
		}
	}
	return blocks, skipped
}

func parseMarkdownFenceOpen(trimmed string) (byte, int, string, bool) {
	if len(trimmed) < 3 {
		return 0, 0, "", false
	}
	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return 0, 0, "", false
	}
	count := 0
	for count < len(trimmed) && trimmed[count] == ch {
		count++
	}
	if count < 3 {
		return 0, 0, "", false
	}
	info := strings.TrimSpace(trimmed[count:])
	if ch == '`' && strings.ContainsRune(info, '`') {
		return 0, 0, "", false
	}
	return ch, count, info, true
}

func isMarkdownFenceClose(trimmed string, fenceChar byte, fenceLen int) bool {
	if len(trimmed) < fenceLen {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != fenceChar {
			return false
		}
	}
	return true
}

func classifySCNFenceInfo(info string) (isSCN bool, skip bool) {
	fields := strings.Fields(strings.ToLower(info))
	if len(fields) == 0 || fields[0] != "scn" {
		return false, false
	}
	for _, token := range fields[1:] {
		switch token {
		case "illustrative", "excerpt", "truncated", "partial", "skip-fmt", "invalid":
			return true, true
		}
	}
	return true, false
}

func docSCNBlockLooksTruncated(content string) bool {
	return strings.Contains(content, "...")
}

func checkDocSCNBlocksWithBinary(ctx context.Context, binaryPath string, blocks []docSCNBlock) ([]checkDiagnostic, error) {
	tmpDir, err := os.MkdirTemp("", "scn-doc-fmt-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	if resolved, err := filepath.EvalSymlinks(tmpDir); err == nil {
		tmpDir = resolved
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "app.scn"), []byte{}, 0o600); err != nil {
		return nil, err
	}
	byFile := make(map[string]docSCNBlock, len(blocks))
	args := []string{"fmt", "--check", "--app-root", tmpDir, "-o", "json"}
	for i, block := range blocks {
		name := fmt.Sprintf("block_%03d.scn", i)
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(block.Body), 0o600); err != nil {
			return nil, err
		}
		byFile[name] = block
		args = append(args, name)
	}
	cmd := commandTreeContext(ctx, binaryPath, args...)
	cmd.Dir = tmpDir
	cmd.Env = scrubSceneryProbeEnv(envpolicy.Environ(), "SCENERY_AGENT_HOME="+filepath.Join(tmpDir, "agent"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if err == nil {
		return nil, nil
	}
	var envelope struct {
		Data struct {
			Changed []string `json:"changed"`
		} `json:"data"`
		Diagnostics []struct {
			Message string `json:"message"`
		} `json:"diagnostics"`
	}
	if json.Unmarshal(stdout.Bytes(), &envelope) == nil && len(envelope.Data.Changed) > 0 {
		diagnostics := make([]checkDiagnostic, 0, len(envelope.Data.Changed))
		for _, changed := range envelope.Data.Changed {
			block, ok := byFile[filepath.Base(changed)]
			if !ok {
				continue
			}
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "scn doc format",
				Severity:        "error",
				File:            block.DocRel,
				Line:            block.Line,
				Message:         "fenced ```scn block in documentation is not canonically formatted",
				SuggestedAction: "Format the `.scn` snippet with `scenery fmt` (or mark illustrative/partial excerpts with ```scn illustrative).",
			})
		}
		if len(diagnostics) > 0 {
			return diagnostics, nil
		}
	}
	detail := tailString(firstNonEmpty(stderr.String(), stdout.String()), 4096)
	if len(envelope.Diagnostics) > 0 && envelope.Diagnostics[0].Message != "" {
		detail = envelope.Diagnostics[0].Message
	}
	return []checkDiagnostic{{
		Stage:           "scn doc format",
		Severity:        "error",
		Message:         fmt.Sprintf("scenery fmt --check failed on documentation .scn blocks: %s", detail),
		SuggestedAction: "Fix invalid or unformatted ```scn blocks in documentation (or mark illustrative/partial excerpts with ```scn illustrative).",
	}}, nil
}
