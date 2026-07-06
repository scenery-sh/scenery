package codegen

import (
	"fmt"
	"go/types"
	"slices"
	"strings"
)

type imports struct {
	current string
	entries map[string]string
	aliases map[string]string
}

type importEntry struct {
	alias string
	path  string
}

func newImports(current string) *imports {
	return &imports{
		current: current,
		entries: make(map[string]string),
		aliases: make(map[string]string),
	}
}

func (im *imports) use(alias, path string) string {
	if existing, ok := im.entries[alias]; ok && existing == path {
		return alias
	}
	if existing, ok := im.aliases[path]; ok {
		return existing
	}
	base := alias
	if base == "" {
		base = pathBase(path)
	}
	final := base
	for i := 2; ; i++ {
		if existing, ok := im.entries[final]; !ok || existing == path {
			break
		}
		final = fmt.Sprintf("%s%d", base, i)
	}
	im.entries[final] = path
	im.aliases[path] = final
	return final
}

func (im *imports) typeExpr(t types.Type) string {
	return types.TypeString(t, func(pkg *types.Package) string {
		if pkg == nil || pkg.Path() == im.current {
			return ""
		}
		return im.use(pkg.Name(), pkg.Path())
	})
}

func (im *imports) sorted() []importEntry {
	items := make([]importEntry, 0, len(im.entries))
	for alias, path := range im.entries {
		items = append(items, importEntry{alias: alias, path: path})
	}
	slices.SortFunc(items, func(a, b importEntry) int {
		return strings.Compare(a.path, b.path)
	})
	return items
}

func pathBase(path string) string {
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		return path[idx+1:]
	}
	return path
}
