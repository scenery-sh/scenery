package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"scenery.sh/internal/repoinfo"
)

const (
	maxInspectDocsExcerptBytes = 2048
	maxInspectDocsTextBytes    = 8192
)

func buildInspectDocsPathsRoute(root string, paths []string, documents []inspectDocsDocument, agents inspectDocsAgents) (inspectDocsPathRoute, error) {
	merged := inspectDocsPathRoute{AgentScopes: []inspectDocsAgentScope{}, Documents: []inspectDocsDocument{}}
	scopes := map[string]bool{}
	docs := map[string]int{}
	for _, path := range paths {
		route, err := buildInspectDocsPathRoute(root, path, documents, agents)
		if err != nil {
			return merged, err
		}
		for _, scope := range route.AgentScopes {
			if !scopes[scope.Path] {
				merged.AgentScopes = append(merged.AgentScopes, scope)
				scopes[scope.Path] = true
			}
		}
		for _, doc := range route.Documents {
			if index, exists := docs[doc.Path]; exists {
				current := &merged.Documents[index]
				for _, section := range doc.Sections {
					found := false
					for _, prior := range current.Sections {
						if prior.Anchor == section.Anchor {
							found = true
							break
						}
					}
					if !found {
						current.Sections = append(current.Sections, section)
					}
				}
			} else {
				docs[doc.Path] = len(merged.Documents)
				merged.Documents = append(merged.Documents, doc)
			}
		}
		merged.VerificationCommands = append(merged.VerificationCommands, route.VerificationCommands...)
	}
	sort.Slice(merged.AgentScopes, func(i, j int) bool { return merged.AgentScopes[i].Path < merged.AgentScopes[j].Path })
	for i := range merged.Documents {
		sort.Slice(merged.Documents[i].Sections, func(a, b int) bool {
			return merged.Documents[i].Sections[a].StartLine < merged.Documents[i].Sections[b].StartLine
		})
	}
	commands := stringSet(merged.VerificationCommands)
	if _, full := commands[harnessValidationFullCommand]; full {
		delete(commands, harnessValidationQuickCommand)
	}
	merged.VerificationCommands = sortedStringSetMap(commands)
	return merged, nil
}

// These renderers have a normative owner. Generic "runtime" keyword matches
// must not redirect their contract lookup to the development supervisor.
func inspectDocsTypeScriptAnchors(path string) []string {
	if !strings.HasPrefix(path, "internal/generate/generate_typescript") || strings.Contains(path, "dev_runtime") {
		return nil
	}
	if strings.Contains(path, "runtime") {
		switch {
		case strings.Contains(path, "_http"):
			return []string{"12-operation-outcomes", "13-client-api", "14-cancellation-time-and-retries"}
		case strings.Contains(path, "_encoding"):
			return []string{"7-scalar-type-mapping", "8-composite-type-mapping"}
		case strings.Contains(path, "_json"):
			return []string{"7-scalar-type-mapping", "8-composite-type-mapping", "9-records-and-unknown-fields", "11-tagged-unions"}
		default:
			return []string{"7-scalar-type-mapping", "8-composite-type-mapping", "9-records-and-unknown-fields", "12-operation-outcomes", "13-client-api", "14-cancellation-time-and-retries"}
		}
	}
	return []string{"3-client-target", "4-artifact-identity", "5-files-and-exports", "17-generation-workflow", "19-conformance-requirements"}
}

func inspectDocsSectionsByAnchor(root, path string, anchors []string) ([]inspectDocsSection, error) {
	sections, err := readInspectDocsMarkdownSections(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return nil, err
	}
	selected := make([]inspectDocsSection, 0, len(anchors))
	for _, anchor := range anchors {
		found := false
		for _, section := range sections {
			if section.Anchor == anchor {
				selected = append(selected, section.Section)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("documentation route references missing section %s#%s", path, anchor)
		}
	}
	return selected, nil
}

func attachInspectDocsExcerpts(root string, documents []inspectDocsDocument) error {
	remaining := maxInspectDocsTextBytes
	for i := range documents {
		doc := &documents[i]
		if len(doc.Sections) == 0 && doc.Role == "active_execplan" {
			sections, err := readInspectDocsMarkdownSections(filepath.Join(root, filepath.FromSlash(doc.Path)))
			if err != nil {
				return err
			}
			for _, section := range sections {
				if section.Anchor == "resume-here" {
					doc.Sections = []inspectDocsSection{section.Section}
					break
				}
			}
		}
		if len(doc.Sections) == 0 && doc.Role != "direct" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(doc.Path)))
		if err != nil {
			return err
		}
		if !utf8.Valid(data) {
			return fmt.Errorf("documentation excerpt source is not UTF-8: %s", doc.Path)
		}
		revision := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
		lines := strings.SplitAfter(string(data), "\n")
		if len(doc.Sections) == 0 {
			doc.Sections = []inspectDocsSection{{Heading: doc.Title, Anchor: "", StartLine: 1, EndLine: len(lines)}}
		}
		for j := range doc.Sections {
			section := &doc.Sections[j]
			if section.StartLine < 1 || section.EndLine < section.StartLine || section.EndLine > len(lines) {
				return fmt.Errorf("invalid documentation span in %s", doc.Path)
			}
			limit := min(remaining, maxInspectDocsExcerptBytes)
			var text strings.Builder
			end := section.StartLine - 1
			for n := section.StartLine - 1; n < section.EndLine; n++ {
				if text.Len()+len(lines[n]) > limit {
					break
				}
				text.WriteString(lines[n])
				end = n + 1
			}
			section.Excerpt = &repoinfo.Excerpt{Text: text.String(), EndLine: end, Truncated: end < section.EndLine, ContentRevision: revision}
			remaining -= text.Len()
		}
	}
	return nil
}
