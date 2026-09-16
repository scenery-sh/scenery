package main

import "scenery.sh/internal/watchignore"

var parseGoEmbedPatterns = watchignore.ParseEmbedPatterns
var shouldIgnoreWatchPathWithMatcher = watchignore.IgnorePath

// shouldIgnoreWatchEntryWithMatcher decides an entry of walkWatchTree, which
// loads each directory's ignore file before visiting its entries and never
// visits the entries of an ignored directory.
var shouldIgnoreWatchEntryWithMatcher = watchignore.IgnoreEntryPath
var isWatchedRootDotFile = watchignore.IsRootDotFile
var hasHiddenOrUnderscorePart = watchignore.HasHiddenPart
