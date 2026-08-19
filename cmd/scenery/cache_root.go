package main

import "scenery.sh/internal/devcache"

func commandCacheRoot() (string, error) {
	return devcache.Root()
}
