package main

import "scenery.sh/internal/watchignore"

var parseGoEmbedPatterns = watchignore.ParseEmbedPatterns
var shouldIgnoreWatchPathWithMatcher = watchignore.IgnorePath
var isWatchedRootDotFile = watchignore.IsRootDotFile
var hasHiddenOrUnderscorePart = watchignore.HasHiddenPart
