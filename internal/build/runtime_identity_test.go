package build

import (
	"os"
	"strings"
	"testing"

	"scenery.sh/internal/machine"
)

func TestCandidateIdentityVerifiesInputsAndProducer(t *testing.T) {
	a := "sha256:" + strings.Repeat("a", 64)
	b := "sha256:" + strings.Repeat("b", 64)
	c := "sha256:" + strings.Repeat("c", 64)
	makeBundle := func() RuntimeBundleDescriptor {
		return RuntimeBundleDescriptor{ArtifactIdentity: machine.NewArtifactIdentity(runtimeBundleKind, runtimeBundleSchemaDescriptor), Target: "development", ContractRevision: a, ImplementationRevision: b, BuildInput: newBuildInputManifest("development", map[string]string{"framework/scenery.sh/source": b, "producer/scenery-cli/executable": c})}
	}
	selection := FrameworkSelection{Source: FrameworkSource{Digest: b}, ExecutableDigest: c}
	identity, err := candidateIdentity(makeBundle(), selection)
	if err != nil || identity.FrameworkExecutableDigest != c {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
	for name, mutate := range map[string]func(*RuntimeBundleDescriptor){
		"schema":       func(b *RuntimeBundleDescriptor) { b.SchemaRevision = a },
		"spec":         func(b *RuntimeBundleDescriptor) { b.SpecRevision = a },
		"input schema": func(b *RuntimeBundleDescriptor) { b.BuildInput.SchemaRevision = a },
		"input spec":   func(b *RuntimeBundleDescriptor) { b.BuildInput.SpecRevision = a },
		"digest":       func(b *RuntimeBundleDescriptor) { b.BuildInput.Digest = a },
		"entry":        func(b *RuntimeBundleDescriptor) { b.BuildInput.Entries[0].Digest = a },
		"order": func(b *RuntimeBundleDescriptor) {
			b.BuildInput.Entries[0], b.BuildInput.Entries[1] = b.BuildInput.Entries[1], b.BuildInput.Entries[0]
		},
		"target":   func(b *RuntimeBundleDescriptor) { b.Target = "other" },
		"producer": func(b *RuntimeBundleDescriptor) { b.Producer.Version = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			bundle := makeBundle()
			mutate(&bundle)
			if _, err := candidateIdentity(bundle, selection); err == nil {
				t.Fatal("accepted invalid candidate")
			}
		})
	}
	selection.ExecutableDigest = a
	if _, err := candidateIdentity(makeBundle(), selection); err == nil {
		t.Fatal("accepted another executable")
	}
}

func TestRuntimeFrameworkControlAcceptsRetainedProducerFromNewBootstrap(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := digestExecutable(executable)
	if err != nil {
		t.Fatal(err)
	}
	selection := FrameworkSelection{
		ArtifactIdentity: machine.NewArtifactIdentity(frameworkSelectionKind, frameworkSelectionSchema),
		AppRoot:          t.TempDir(),
		Source: FrameworkSource{
			Digest: "sha256:" + strings.Repeat("a", 64),
			Inputs: &BuildInputManifest{ArtifactIdentity: machine.NewArtifactIdentity(buildInputKind, buildInputSchemaDescriptor)},
		},
		Executable:       executable,
		ExecutableDigest: digest,
	}
	if err := VerifyRuntimeFrameworkControl(selection); err != nil {
		t.Fatalf("VerifyRuntimeFrameworkControl() error = %v", err)
	}
	if err := VerifyRuntimeFramework(selection); err == nil {
		t.Fatal("strict runtime verification accepted a non-owner bootstrap")
	}
}
