package main

import "scenery.sh/internal/watchignore"

var parseGoEmbedPatterns = watchignore.ParseEmbedPatterns
var shouldIgnoreWatchPathWithMatcher = watchignore.IgnorePath
var hasHiddenOrUnderscorePart = watchignore.HasHiddenPart
