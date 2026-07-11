package vnext

import "strings"

func legacyResource(module, kind, name string, spec map[string]any, origin Origin, meta *MigrationMeta, semantics, contract string) Resource {
	// Static lowering can verify shape, but only executed behavioral fixtures
	// can establish exact migration equivalence.
	if semantics == "legacy_exact" {
		semantics = "advisory"
	}
	disposition := "advisory"
	if contract == "opaque" {
		disposition = "opaque"
	} else if contract == "unsupported" {
		disposition = "unsupported"
	}
	return Resource{
		Address: resourceAddress(module, kind, name), Kind: "scenery." + strings.ReplaceAll(kind, "_", "-") + "/v1", Name: name, Module: module, Spec: spec, Origin: origin, Migration: meta,
		Compatibility: &LegacyCompatibility{Semantics: semantics, Contract: contract, MigrationDisposition: disposition},
	}
}

func legacyResourceOrigin(module, symbol, construct string) Origin {
	return Origin{Kind: "legacy_v0", Frontend: "scenery.legacy.v0", SourceID: "src_legacy_" + shortStableID(module+"\x00"+symbol+"\x00"+construct), LegacySymbol: symbol, LegacyConstruct: construct}
}
