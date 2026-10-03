package main

import "strings"

type instructionMarkdownBlock struct {
	text string
	line int
}

// Treat soft line wrapping as prose while keeping unrelated paragraphs, list
// items and fenced commands from qualifying each other's authorization.
func instructionMarkdownBlocks(text string) []instructionMarkdownBlock {
	var blocks []instructionMarkdownBlock
	var lines []string
	start := 0
	flush := func() {
		if len(lines) > 0 {
			blocks = append(blocks, instructionMarkdownBlock{strings.Join(strings.Fields(strings.Join(lines, " ")), " "), start})
			lines = nil
		}
	}
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			flush()
		}
		if len(lines) == 0 {
			start = i + 1
		}
		lines = append(lines, trimmed)
	}
	flush()
	return blocks
}
