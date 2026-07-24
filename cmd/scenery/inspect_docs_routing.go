package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	maxInspectDocsPathDocuments = 8
	maxInspectDocsContractDocs  = 3
	maxInspectDocsPlanDocs      = 1
	maxInspectDocsSchemaDocs    = 2
)

type inspectDocsPathRoute struct {
	AgentScopes          []inspectDocsAgentScope
	Documents            []inspectDocsDocument
	VerificationCommands []string
}

type inspectDocsMarkdownSection struct {
	inspectDocsSection
	Level int
	Text  string
}

type inspectDocsTermProfile struct {
	Primary map[string]bool
	Domain  map[string]bool
}

type inspectDocsScoredDocument struct {
	Document inspectDocsDocument
	Score    int
}

func validateInspectDocsOptions(opts inspectDocsOptions) error {
	if opts.Status != "" {
		switch strings.ToLower(strings.TrimSpace(opts.Status)) {
		case "active", "reference", "completed", "deprecated":
		default:
			return fmt.Errorf("--status must be active, reference, completed, or deprecated")
		}
	}
	filterCount := 0
	if strings.TrimSpace(opts.Tag) != "" {
		filterCount++
	}
	if strings.TrimSpace(opts.Status) != "" {
		filterCount++
	}
	if opts.ReviewDue {
		filterCount++
	}
	if opts.All && (strings.TrimSpace(opts.ForPath) != "" || filterCount > 0) {
		return fmt.Errorf("--all cannot be combined with --for-path, --tag, --status, or --review-due")
	}
	if strings.TrimSpace(opts.ForPath) != "" && filterCount > 0 {
		return fmt.Errorf("--for-path cannot be combined with --tag, --status, or --review-due")
	}
	return nil
}

func buildInspectDocsQuery(repoRoot string, opts inspectDocsOptions) (inspectDocsQuery, error) {
	switch {
	case opts.All:
		return inspectDocsQuery{Mode: "all", All: true}, nil
	case strings.TrimSpace(opts.ForPath) != "":
		path, err := normalizeInspectDocsQueryPath(repoRoot, opts.ForPath)
		if err != nil {
			return inspectDocsQuery{}, err
		}
		return inspectDocsQuery{Mode: "path", ForPath: path}, nil
	case strings.TrimSpace(opts.Tag) != "" || strings.TrimSpace(opts.Status) != "" || opts.ReviewDue:
		return inspectDocsQuery{
			Mode:      "filter",
			Tag:       strings.ToLower(strings.TrimSpace(opts.Tag)),
			Status:    strings.ToLower(strings.TrimSpace(opts.Status)),
			ReviewDue: opts.ReviewDue,
		}, nil
	default:
		return inspectDocsQuery{Mode: "summary"}, nil
	}
}

func normalizeInspectDocsQueryPath(repoRoot, raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("--for-path contains NUL")
	}
	value := filepath.Clean(filepath.FromSlash(strings.TrimSpace(raw)))
	if filepath.IsAbs(value) {
		rel, err := filepath.Rel(repoRoot, value)
		if err != nil {
			return "", fmt.Errorf("resolve --for-path: %w", err)
		}
		value = rel
	}
	value = filepath.ToSlash(filepath.Clean(value))
	if value == ".." || strings.HasPrefix(value, "../") {
		return "", fmt.Errorf("--for-path must stay within the repository")
	}
	if value == "" {
		return "", fmt.Errorf("--for-path must not be empty")
	}
	return value, nil
}

func filterInspectDocsDocuments(documents []inspectDocsDocument, query inspectDocsQuery) []inspectDocsDocument {
	selected := make([]inspectDocsDocument, 0)
	for _, doc := range documents {
		if query.Status != "" && doc.Status != query.Status {
			continue
		}
		// Historical plans stay out of ordinary catalog discovery. A stale
		// entry is an explicit index signal that the history contradicts the
		// current contract; --status completed remains the intentional archive
		// query.
		if query.Status == "" && isCompletedExecPlanDocument(doc.docsKnowledgeDocument) && !doc.Stale {
			continue
		}
		if query.ReviewDue && !doc.ReviewDue {
			continue
		}
		if query.Tag != "" && !inspectDocsHasTag(doc.Tags, query.Tag) {
			continue
		}
		selected = append(selected, doc)
	}
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].Path < selected[j].Path
	})
	return selected
}

func inspectDocsHasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), want) {
			return true
		}
	}
	return false
}

func buildInspectDocsPathRoute(repoRoot, targetPath string, documents []inspectDocsDocument, agents inspectDocsAgents) (inspectDocsPathRoute, error) {
	route := inspectDocsPathRoute{
		AgentScopes:          applicableInspectDocsAgentScopes(targetPath, agents.Scopes),
		Documents:            []inspectDocsDocument{},
		VerificationCommands: []string{},
	}
	documentByPath := make(map[string]inspectDocsDocument, len(documents))
	for _, doc := range documents {
		documentByPath[doc.Path] = doc
	}

	packages, _ := inspectDocsGoPackagesForPath(context.Background(), repoRoot, targetPath)
	changedArea := &harnessChangedAreaReport{
		cliPayloadIdentity:  newCLIPayloadIdentity(harnessChangedAreaKind),
		ChangedFiles:        []harnessChangedFile{},
		AffectedPackages:    []string{},
		RecommendedCommands: []string{},
		RelevantDocs:        []string{},
		RiskFlags:           []string{},
		Diagnostics:         []checkDiagnostic{},
	}
	populateHarnessChangedAreaReport(repoRoot, changedArea, []harnessChangedFile{{
		Path:   targetPath,
		Status: "prospective",
	}}, packages, nil)

	commandSet := stringSet(changedArea.RecommendedCommands)
	delete(commandSet, "scenery harness self --summary --write")
	commandSet[".scenery/harness/bin/scenery harness self --summary --write"] = struct{}{}
	if len(route.AgentScopes) > 1 {
		nearest := route.AgentScopes[len(route.AgentScopes)-1]
		for _, command := range inspectDocsVerificationCommands(repoRoot, nearest.Path) {
			commandSet[command] = struct{}{}
		}
	}
	route.VerificationCommands = sortedStringSetMap(commandSet)

	profile := inspectDocsTermProfile{
		Primary: inspectDocsTerms(targetPath),
		Domain:  map[string]bool{},
	}
	if len(route.AgentScopes) > 0 {
		nearest := route.AgentScopes[len(route.AgentScopes)-1]
		mergeInspectDocsTerms(profile.Domain, inspectDocsTerms(inspectDocsAgentPurposeText(repoRoot, nearest.Path)))
	}

	selected := map[string]bool{}
	add := func(doc inspectDocsDocument, role, reason string, sections []inspectDocsSection) {
		if selected[doc.Path] || len(route.Documents) >= maxInspectDocsPathDocuments {
			return
		}
		// Scoped discovery names related schemas separately; carrying a broad
		// contract document's complete schema_refs list defeats the response
		// budget without adding another document to read.
		doc.SchemaRefs = nil
		doc.SizeBytes = 0
		doc.ModifiedAt = ""
		doc.Role = role
		doc.Reason = reason
		doc.Sections = sections
		route.Documents = append(route.Documents, doc)
		selected[doc.Path] = true
	}

	if direct, ok := documentByPath[targetPath]; ok {
		add(direct, "direct", "queried path is an indexed document", nil)
	}

	architectureSections, architectureText := inspectDocsArchitectureSections(repoRoot, targetPath)
	mergeInspectDocsTerms(profile.Domain, inspectDocsTerms(architectureText))
	if doc, ok := documentByPath["ARCHITECTURE.md"]; ok && len(architectureSections) > 0 {
		add(doc, "architecture", "owning architecture section", architectureSections)
	}

	relevantSet := stringSet(changedArea.RelevantDocs)
	contracts := inspectDocsContractMatches(repoRoot, profile, documents, relevantSet)
	for _, match := range contracts {
		add(match.Document, "contract", "current contract section matched to the path", match.Document.Sections)
	}

	plans := inspectDocsPlanMatches(profile, documents, relevantSet)
	for _, match := range plans {
		add(match.Document, "active_execplan", "active ExecPlan matched to the path", nil)
	}

	schemas := inspectDocsSchemaMatches(profile, documents, route.Documents, relevantSet)
	for _, match := range schemas {
		add(match.Document, "schema", "related machine contract schema", nil)
	}
	return route, nil
}

func inspectDocsGoPackagesForPath(ctx context.Context, repoRoot, targetPath string) ([]harnessPackageInfo, error) {
	if !strings.EqualFold(filepath.Ext(targetPath), ".go") {
		return nil, nil
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		return nil, err
	}
	relDir := filepath.ToSlash(filepath.Dir(targetPath))
	pattern := "."
	if relDir != "." {
		pattern = "./" + strings.TrimPrefix(relDir, "./")
	}
	cmd := commandTreeContext(ctx, goPath, "list", "-find", "-f", "{{.ImportPath}}\t{{.Dir}}", pattern)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go list package for %s: %w: %s", targetPath, err, strings.TrimSpace(string(output)))
	}
	fields := strings.SplitN(strings.TrimSpace(string(output)), "\t", 2)
	if len(fields) != 2 || strings.TrimSpace(fields[0]) == "" || strings.TrimSpace(fields[1]) == "" {
		return nil, fmt.Errorf("go list package for %s returned malformed output", targetPath)
	}
	dir := filepath.Clean(strings.TrimSpace(fields[1]))
	rel, err := filepath.Rel(repoRoot, dir)
	if err != nil {
		rel = dir
	}
	return []harnessPackageInfo{{
		ImportPath: strings.TrimSpace(fields[0]),
		Dir:        dir,
		RelDir:     filepath.ToSlash(filepath.Clean(rel)),
	}}, nil
}

func applicableInspectDocsAgentScopes(targetPath string, scopes []inspectDocsAgentScope) []inspectDocsAgentScope {
	applicable := make([]inspectDocsAgentScope, 0)
	for _, scope := range scopes {
		if scope.Scope == "." || targetPath == scope.Scope || strings.HasPrefix(targetPath, strings.TrimSuffix(scope.Scope, "/")+"/") {
			applicable = append(applicable, scope)
		}
	}
	sort.Slice(applicable, func(i, j int) bool {
		leftDepth := strings.Count(applicable[i].Scope, "/")
		rightDepth := strings.Count(applicable[j].Scope, "/")
		if leftDepth == rightDepth {
			return applicable[i].Path < applicable[j].Path
		}
		return leftDepth < rightDepth
	})
	return applicable
}

func inspectDocsArchitectureSections(repoRoot, targetPath string) ([]inspectDocsSection, string) {
	sections, err := readInspectDocsMarkdownSections(filepath.Join(repoRoot, "ARCHITECTURE.md"))
	if err != nil {
		return nil, ""
	}
	bestIndex := -1
	bestPrefix := -1
	for i, section := range sections {
		for _, candidate := range inspectDocsBacktickValues(section.Heading + "\n" + section.Text) {
			candidate = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(filepath.FromSlash(candidate))), "/")
			if candidate == "." || candidate == "" {
				continue
			}
			if targetPath == candidate || strings.HasPrefix(targetPath, candidate+"/") {
				if len(candidate) > bestPrefix {
					bestIndex = i
					bestPrefix = len(candidate)
				}
			}
		}
	}
	if bestIndex < 0 {
		profile := inspectDocsTermProfile{Primary: inspectDocsTerms(targetPath), Domain: map[string]bool{}}
		bestScore := 0
		for i, section := range sections {
			score := inspectDocsTextScore(profile, section.Heading+"\n"+section.Text)
			if score > bestScore {
				bestIndex = i
				bestScore = score
			}
		}
	}
	if bestIndex < 0 {
		return nil, ""
	}
	section := sections[bestIndex]
	return []inspectDocsSection{section.inspectDocsSection}, section.Heading + "\n" + section.Text
}

func inspectDocsAgentPurposeText(repoRoot, relPath string) string {
	sections, err := readInspectDocsMarkdownSections(filepath.Join(repoRoot, filepath.FromSlash(relPath)))
	if err != nil {
		return ""
	}
	for _, section := range sections {
		if strings.EqualFold(strings.TrimSpace(section.Heading), "Purpose") {
			return section.Heading + "\n" + section.Text
		}
	}
	return ""
}

func inspectDocsContractMatches(repoRoot string, profile inspectDocsTermProfile, documents []inspectDocsDocument, relevant map[string]struct{}) []inspectDocsScoredDocument {
	matches := make([]inspectDocsScoredDocument, 0)
	for _, doc := range documents {
		if doc.Status != "active" && doc.Status != "reference" {
			continue
		}
		if doc.Path == "ARCHITECTURE.md" || strings.HasSuffix(doc.Path, "AGENTS.md") || strings.HasPrefix(doc.Path, "docs/plans/") || strings.HasPrefix(doc.Path, "docs/schemas/") {
			continue
		}
		_, forced := relevant[doc.Path]
		if !forced && doc.Path != "docs/local-contract.md" && doc.Path != "docs/agent-guide.md" && !strings.HasPrefix(doc.Path, "docs/spec/") {
			continue
		}
		if !forced && doc.Path == "docs/spec/SPEC.md" {
			continue
		}
		if !forced && strings.HasPrefix(doc.Path, "docs/spec/") && inspectDocsPrimaryMatchCount(profile, doc.Path+" "+doc.Title) == 0 {
			continue
		}
		sections, err := readInspectDocsMarkdownSections(filepath.Join(repoRoot, filepath.FromSlash(doc.Path)))
		if err != nil {
			continue
		}
		selectedSections, sectionScore := inspectDocsBestSections(profile, sections, 2)
		metadataScore := inspectDocsTextScore(profile, doc.Path+" "+doc.Title+" "+doc.Owner+" "+doc.Summary+" "+strings.Join(doc.Tags, " "))
		score := metadataScore + sectionScore
		if forced {
			score += 100
		}
		if score < 8 || len(selectedSections) == 0 {
			continue
		}
		doc.Sections = selectedSections
		matches = append(matches, inspectDocsScoredDocument{Document: doc, Score: score})
	}
	sortInspectDocsMatches(matches)
	if len(matches) > maxInspectDocsContractDocs {
		matches = matches[:maxInspectDocsContractDocs]
	}
	return matches
}

func inspectDocsPlanMatches(profile inspectDocsTermProfile, documents []inspectDocsDocument, relevant map[string]struct{}) []inspectDocsScoredDocument {
	matches := make([]inspectDocsScoredDocument, 0)
	for _, doc := range documents {
		if doc.Status != "active" || !inspectDocsExecPlanPathPattern.MatchString(doc.Path) {
			continue
		}
		score := inspectDocsTextScore(profile, doc.Path+" "+doc.Title+" "+doc.Owner+" "+doc.Summary+" "+strings.Join(doc.Tags, " "))
		_, forced := relevant[doc.Path]
		if forced {
			score += 100
		}
		if (!forced && inspectDocsPrimaryMatchCount(profile, doc.Path+" "+doc.Title+" "+strings.Join(doc.Tags, " ")) == 0) || score < 8 {
			continue
		}
		matches = append(matches, inspectDocsScoredDocument{Document: doc, Score: score})
	}
	sortInspectDocsMatches(matches)
	if len(matches) > maxInspectDocsPlanDocs {
		matches = matches[:maxInspectDocsPlanDocs]
	}
	return matches
}

func inspectDocsSchemaMatches(profile inspectDocsTermProfile, documents, selected []inspectDocsDocument, relevant map[string]struct{}) []inspectDocsScoredDocument {
	candidatePaths := map[string]bool{}
	for _, doc := range documents {
		if strings.HasPrefix(doc.Path, "docs/schemas/") {
			candidatePaths[doc.Path] = true
		}
	}
	for _, doc := range selected {
		for _, schemaRef := range doc.SchemaRefs {
			candidatePaths[schemaRef] = true
		}
	}
	for path := range relevant {
		if strings.HasPrefix(path, "docs/schemas/") {
			candidatePaths[path] = true
		}
	}
	matches := make([]inspectDocsScoredDocument, 0)
	maxPrimaryMatches := 0
	for _, doc := range documents {
		if !candidatePaths[doc.Path] {
			continue
		}
		score := inspectDocsTextScore(profile, doc.Path+" "+doc.Title+" "+doc.Owner+" "+doc.Summary+" "+strings.Join(doc.Tags, " "))
		primaryMatches := inspectDocsPrimaryMatchCount(profile, doc.Path+" "+doc.Title+" "+strings.Join(doc.Tags, " "))
		if _, forced := relevant[doc.Path]; forced {
			score += 100
		}
		if score < 6 {
			continue
		}
		if primaryMatches > maxPrimaryMatches {
			maxPrimaryMatches = primaryMatches
		}
		matches = append(matches, inspectDocsScoredDocument{Document: doc, Score: score})
	}
	if maxPrimaryMatches > 1 {
		filtered := matches[:0]
		for _, match := range matches {
			if inspectDocsPrimaryMatchCount(profile, match.Document.Path+" "+match.Document.Title+" "+strings.Join(match.Document.Tags, " ")) == maxPrimaryMatches {
				filtered = append(filtered, match)
			}
		}
		matches = filtered
	}
	sortInspectDocsMatches(matches)
	if len(matches) > maxInspectDocsSchemaDocs {
		matches = matches[:maxInspectDocsSchemaDocs]
	}
	return matches
}

func sortInspectDocsMatches(matches []inspectDocsScoredDocument) {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Document.Path < matches[j].Document.Path
		}
		return matches[i].Score > matches[j].Score
	})
}

func inspectDocsBestSections(profile inspectDocsTermProfile, sections []inspectDocsMarkdownSection, limit int) ([]inspectDocsSection, int) {
	type scoredSection struct {
		Section inspectDocsMarkdownSection
		Score   int
	}
	scored := make([]scoredSection, 0)
	hasNested := false
	for _, section := range sections {
		if section.Level > 1 {
			hasNested = true
			break
		}
	}
	for _, section := range sections {
		if hasNested && section.Level == 1 {
			continue
		}
		headingScore := inspectDocsTextScore(profile, section.Heading)
		bodyScore := inspectDocsTextScore(profile, section.Text)
		score := headingScore*3 + bodyScore
		if score < 4 {
			continue
		}
		scored = append(scored, scoredSection{Section: section, Score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Section.StartLine < scored[j].Section.StartLine
		}
		return scored[i].Score > scored[j].Score
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	selected := make([]inspectDocsSection, 0, len(scored))
	total := 0
	for _, item := range scored {
		selected = append(selected, item.Section.inspectDocsSection)
		total += item.Score
	}
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].StartLine < selected[j].StartLine
	})
	return selected, total
}

func readInspectDocsMarkdownSections(path string) ([]inspectDocsMarkdownSection, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	sections := make([]inspectDocsMarkdownSection, 0)
	for i, line := range lines {
		level, heading, ok := inspectDocsMarkdownHeading(line)
		if !ok {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if _, _, next := inspectDocsMarkdownHeading(lines[j]); next {
				end = j
				break
			}
		}
		sections = append(sections, inspectDocsMarkdownSection{
			inspectDocsSection: inspectDocsSection{
				Heading:   heading,
				Anchor:    inspectDocsMarkdownAnchor(heading),
				StartLine: i + 1,
				EndLine:   end,
			},
			Level: level,
			Text:  strings.Join(lines[i+1:end], "\n"),
		})
	}
	return sections, nil
}

func inspectDocsMarkdownHeading(line string) (int, string, bool) {
	trimmed := strings.TrimSpace(line)
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
		return 0, "", false
	}
	heading := strings.TrimSpace(trimmed[level+1:])
	if heading == "" {
		return 0, "", false
	}
	return level, heading, true
}

func inspectDocsMarkdownAnchor(heading string) string {
	heading = strings.ToLower(strings.ReplaceAll(heading, "`", ""))
	var b strings.Builder
	lastDash := false
	for _, r := range heading {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case unicode.IsSpace(r) || r == '-' || r == '_':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

var inspectDocsBacktickPattern = regexp.MustCompile("`([^`]+)`")

func inspectDocsBacktickValues(value string) []string {
	matches := inspectDocsBacktickPattern.FindAllStringSubmatch(value, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			out = append(out, match[1])
		}
	}
	return out
}

func inspectDocsVerificationCommands(repoRoot, relPath string) []string {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(relPath)))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	inRelevantSection := false
	inFence := false
	commands := []string{}
	for _, line := range lines {
		if _, heading, ok := inspectDocsMarkdownHeading(line); ok {
			lower := strings.ToLower(heading)
			inRelevantSection = strings.Contains(lower, "verification") || strings.Contains(lower, "validation")
			inFence = false
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inRelevantSection {
				inFence = !inFence
			}
			continue
		}
		if !inRelevantSection || !inFence || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		commands = append(commands, trimmed)
	}
	return uniqueSortedStrings(commands)
}

func inspectDocsTextScore(profile inspectDocsTermProfile, text string) int {
	terms := inspectDocsTerms(text)
	score := 0
	for term := range terms {
		if profile.Primary[term] {
			score += 4
			continue
		}
		if profile.Domain[term] {
			score++
		}
	}
	return score
}

func inspectDocsPrimaryMatchCount(profile inspectDocsTermProfile, text string) int {
	terms := inspectDocsTerms(text)
	count := 0
	for term := range terms {
		if profile.Primary[term] {
			count++
		}
	}
	return count
}

func inspectDocsTerms(value string) map[string]bool {
	value = inspectDocsCamelBoundary.ReplaceAllString(value, "${1} ${2}")
	fields := inspectDocsWordPattern.FindAllString(strings.ToLower(value), -1)
	terms := map[string]bool{}
	for _, field := range fields {
		term := canonicalInspectDocsTerm(field)
		if term == "" || inspectDocsStopWords[term] {
			continue
		}
		terms[term] = true
	}
	return terms
}

func mergeInspectDocsTerms(dst, src map[string]bool) {
	for term := range src {
		dst[term] = true
	}
}

func sortedStringSetMap(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

var (
	inspectDocsCamelBoundary       = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	inspectDocsWordPattern         = regexp.MustCompile(`[a-z0-9]+`)
	inspectDocsExecPlanPathPattern = regexp.MustCompile(`^docs/plans/[0-9]{4}-.+\.md$`)
	inspectDocsStopWords           = map[string]bool{
		"a": true, "an": true, "and": true, "app": true, "application": true,
		"apps": true, "as": true, "at": true, "be": true, "by": true, "cmd": true,
		"code": true, "current": true, "file": true, "for": true, "from": true,
		"docs": true, "go": true, "in": true, "internal": true, "is": true, "it": true,
		"json": true, "jsx": true, "md": true,
		"of": true, "on": true, "one": true, "or": true, "repo": true,
		"repository": true, "root": true, "scenery": true, "sh": true, "src": true,
		"that": true, "the": true, "this": true, "to": true, "under": true,
		"tsx": true, "use": true, "with": true,
	}
)

func canonicalInspectDocsTerm(value string) string {
	switch value {
	case "generated", "generates", "generating", "generation", "generator", "generators":
		return "generate"
	case "clients", "clientgen":
		return "client"
	case "schemas":
		return "schema"
	case "tests", "testing":
		return "test"
	case "execplans":
		return "execplan"
	default:
		return value
	}
}
