package main

import "testing"

func TestWatchCandidateOwnsCapturedBuffers(t *testing.T) {
	retained := []byte("captured")
	other := []byte("tampered")
	stamp := fileStamp{data: retained, hash: "same-declared-hash"}
	snapshot := fileSnapshot{
		files:         map[string]fileStamp{"a.go": stamp},
		compilerFiles: map[string]fileStamp{"a.go": stamp, "b.go": {data: other, hash: stamp.hash}},
		contractFiles: map[string]fileStamp{"a.go": stamp},
	}
	candidate := buildSourceSnapshot(snapshot)
	candidate.Files["a.go"].Data[0] = 'C'
	if string(retained) != "captured" || string(other) != "tampered" {
		t.Fatal("candidate mutation changed retained watch input")
	}
	if string(candidate.CompilerFiles["a.go"].Data) != "Captured" || string(candidate.ContractFiles["a.go"].Data) != "Captured" {
		t.Fatal("overlapping views did not share candidate-owned capture")
	}
	if string(candidate.CompilerFiles["b.go"].Data) != "tampered" {
		t.Fatal("declared hash concealed distinct captured bytes")
	}
}
