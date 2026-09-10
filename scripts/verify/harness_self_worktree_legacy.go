package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/build"
)

const worktreeLegacyRevision = "c56e3e9614e58914e27a8536b171e4d979f32bbb"

// The historical binary is executed only inside a disposable nested daemon.
// Neither the host Docker socket nor any host source directory is mounted.
func (p *worktreeRuntimeProbe) legacyCoexistence() error {
	return p.scenario("A9", "incompatible-home rejection, pre-cutover coexistence and lossless native migration on a disposable daemon", func(e map[string]any) (resultErr error) {
		dir := filepath.Join(p.root, "legacy-sandbox")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		dockerfile := "FROM golang@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452 AS toolchain\nRUN mkdir /glibc && cp -L /lib/*-linux-gnu/ld-linux*.so* /lib/*-linux-gnu/libc.so.6 /lib/*-linux-gnu/libdl.so.2 /lib/*-linux-gnu/libm.so.6 /lib/*-linux-gnu/libpthread.so.0 /lib/*-linux-gnu/librt.so.1 /lib/*-linux-gnu/libresolv.so.2 /glibc/\nFROM docker@sha256:5efed980cba3fc126cf54e21a5a6ff8849d05b6e0623d6e7612f48e9cd6cd17e\nRUN apk add --no-cache procps bash\nCOPY --from=toolchain /usr/local/go /usr/local/go\nCOPY --from=toolchain /glibc/ /lib/\nRUN mkdir -p /lib64 && if test -f /lib/ld-linux-x86-64.so.2; then ln -s /lib/ld-linux-x86-64.so.2 /lib64/ld-linux-x86-64.so.2; fi\nENV PATH=/usr/local/go/bin:$PATH\n"
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o600); err != nil {
			return err
		}
		out, err := p.run(dir, "docker", "build", "--quiet", ".")
		if err != nil {
			return err
		}
		image := strings.TrimSpace(string(out))
		if !strings.HasPrefix(image, "sha256:") {
			return fmt.Errorf("sandbox build did not return an immutable image ID")
		}
		label := "dev.scenery.release.sandbox=" + filepath.Base(p.root)
		out, err = p.run(dir, "docker", "run", "--detach", "--rm", "--privileged", "--label", label, "--entrypoint", "dockerd", image, "--host=unix:///var/run/docker.sock", "--tls=false")
		if err != nil {
			return err
		}
		container := strings.TrimSpace(string(out))
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			check, inspectErr := p.runWithContext(ctx, dir, "docker", "inspect", "--format", `{{.Id}} {{index .Config.Labels "dev.scenery.release.sandbox"}}`, container)
			if inspectErr != nil || strings.TrimSpace(string(check)) != container+" "+filepath.Base(p.root) {
				e["sandbox_cleanup_error"] = "sandbox identity could not be verified; retained for inspection"
				resultErr = errors.Join(resultErr, fmt.Errorf("sandbox cleanup identity verification failed"))
				return
			}
			if resultErr != nil {
				// Private diagnostic copies may include retained credentials.
				// Keep them under this owner-only probe root, never in summaries.
				for _, source := range []string{"/legacy-state", "/candidate-state", "/app-old/.scenery", "/app-new/.scenery"} {
					dest := filepath.Join(dir, "failure-"+strings.ReplaceAll(strings.Trim(source, "/"), "/", "-"))
					_, _ = p.runWithContext(ctx, dir, "docker", "cp", container+":"+source, dest)
				}
			}
			_, stopErr := p.runWithContext(ctx, dir, "docker", "stop", "--time", "10", container)
			if stopErr != nil {
				e["sandbox_cleanup_error"] = stopErr.Error()
				resultErr = errors.Join(resultErr, stopErr)
				return
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				removed, inspectErr := p.runWithContext(ctx, dir, "docker", "inspect", "--format", "{{.Id}}", container)
				if inspectErr != nil && isMissingDockerObject(string(removed), inspectErr) {
					e["sandbox_removed"] = true
					return
				}
				if inspectErr != nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			resultErr = errors.Join(resultErr, fmt.Errorf("sandbox removal was not confirmed"))
		}()
		s := &worktreeLegacySandbox{p: p, container: container}
		deadline := time.Now().Add(time.Minute)
		var nestedID string
		for time.Now().Before(deadline) {
			out, err = s.run("docker", "info", "--format", "{{.ID}}")
			if err == nil {
				nestedID = strings.TrimSpace(string(out))
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		outer, err := p.run(dir, "docker", "info", "--format", "{{.ID}}")
		if err != nil || nestedID == "" || nestedID == strings.TrimSpace(string(outer)) {
			return fmt.Errorf("disposable Docker daemon isolation was not established")
		}
		e["nested_daemon_id"], e["host_daemon_id"], e["sandbox_image"] = nestedID, strings.TrimSpace(string(outer)), image
		if err := p.prepareLegacySources(dir, s, e); err != nil {
			return err
		}
		return s.prove(e)
	})
}

type worktreeLegacySandbox struct {
	p         *worktreeRuntimeProbe
	container string
}

func (s *worktreeLegacySandbox) run(args ...string) ([]byte, error) {
	return s.p.run(s.p.repo, "docker", append([]string{"exec", s.container}, args...)...)
}

func (s *worktreeLegacySandbox) cli(old bool, root string, args ...string) ([]byte, error) {
	home := "/candidate-state"
	if old {
		home = "/legacy-state"
	}
	return s.cliInHome(old, home, root, args...)
}

func (s *worktreeLegacySandbox) cliInHome(old bool, home, root string, args ...string) ([]byte, error) {
	binary := "/candidate-scenery"
	if old {
		binary = "/baseline-scenery"
	}
	env := []string{"env", "SCENERY_AGENT_HOME=" + home, "SCENERY_DEV_VICTORIA=0", "SCENERY_DEV_VICTORIA_DOWNLOAD=0", "SCENERY_AGENT_TRUST=0", "GOWORK=off", binary}
	env = append(env, args...)
	env = append(env, "--app-root", root, "-o", "json")
	return s.run(env...)
}

func (p *worktreeRuntimeProbe) prepareLegacySources(dir string, s *worktreeLegacySandbox, e map[string]any) error {
	sourceBefore, err := p.sourceDigest()
	if err != nil {
		return err
	}
	baseline := filepath.Join(dir, "baseline")
	archive := filepath.Join(dir, "baseline.tar")
	if err := os.MkdirAll(baseline, 0o700); err != nil {
		return err
	}
	if _, err := p.run(p.repo, "git", "archive", "--format=tar", "--output", archive, worktreeLegacyRevision); err != nil {
		return err
	}
	if _, err := p.run(dir, "tar", "-xf", archive, "-C", baseline); err != nil {
		return err
	}
	candidate := filepath.Join(dir, "candidate")
	files, err := p.run(p.repo, "git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	for _, name := range bytes.Split(files, []byte{0}) {
		rel := string(name)
		if rel == "" || strings.HasPrefix(rel, ".scratch/") || filepath.Base(rel) == ".env" {
			continue
		}
		info, err := os.Lstat(filepath.Join(p.repo, rel))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.repo, rel))
		if err != nil {
			return err
		}
		dest := filepath.Join(candidate, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, info.Mode().Perm()); err != nil {
			return err
		}
	}
	originalEnv := p.env
	defer func() { p.env = originalEnv }()
	p.env = envWithOverrides(p.env, "GOOS=linux", "CGO_ENABLED=0", "GOWORK=off")
	provenance := map[string]any{"baseline_revision": worktreeLegacyRevision}
	for _, item := range []struct{ name, source string }{{"baseline", baseline}} {
		binary := filepath.Join(dir, item.name+"-scenery")
		if _, err := p.run(item.source, "go", "build", "-o", binary, "./cmd/scenery"); err != nil {
			return err
		}
		digest, err := worktreeProbeFileSHA(binary)
		if err != nil {
			return err
		}
		provenance[item.name+"_linux_sha256"] = digest
		if _, err := p.run(dir, "docker", "cp", binary, s.container+":/"+item.name+"-scenery"); err != nil {
			return err
		}
	}
	p.env = originalEnv
	for _, item := range []struct{ source, target string }{{baseline, "/baseline"}, {candidate, "/candidate"}} {
		if _, err := p.run(dir, "docker", "cp", item.source, s.container+":"+item.target); err != nil {
			return err
		}
		if item.target == "/baseline" {
			if _, err := s.run("go", "-C", item.target, "mod", "download", "all"); err != nil {
				return err
			}
		}
	}
	// Compile the current producer at its actual sandbox source path. A host
	// cross-build would embed an unavailable host path, and an unstamped CLI
	// must not be allowed to start an application under the coherence contract.
	// Do not run download-all in the candidate: it expands go.sum with unused
	// test dependencies, changing the source after its host-side fingerprint.
	selectedSource, err := build.FrameworkSourceManifest(candidate)
	if err != nil {
		return err
	}
	flags, err := build.FrameworkProducerLinkerFlags(selectedSource.Digest)
	if err != nil {
		return err
	}
	if _, err := s.run("env", "GOWORK=off", "CGO_ENABLED=0", "go", "-C", "/candidate", "build", "-mod=readonly", "-ldflags="+flags, "-o", "/candidate-scenery", "./cmd/scenery"); err != nil {
		return err
	}
	candidateBinary := filepath.Join(dir, "candidate-scenery")
	if _, err := p.run(dir, "docker", "cp", s.container+":/candidate-scenery", candidateBinary); err != nil {
		return err
	}
	candidateHash, err := worktreeProbeFileSHA(candidateBinary)
	if err != nil {
		return err
	}
	provenance["candidate_linux_sha256"] = candidateHash
	provenance["candidate_framework_source_digest"] = selectedSource.Digest
	for _, name := range []string{"old", "sibling", "new", "migration"} {
		legacy := name == "old" || name == "sibling"
		fixture := filepath.Join(dir, "app-"+name)
		for _, rel := range []string{".scenery.json", ".gitignore", "app.scn", "app.lock.scn", "go.mod", "go.sum", "library/service.go", "library/package.scn", "cmd/schema/main.go"} {
			data, err := os.ReadFile(filepath.Join(p.repo, "testdata/apps/worktree-postgres", rel))
			if err != nil {
				return err
			}
			if legacy && rel == ".scenery.json" {
				// Only the historical binary in this disposable sandbox consumes
				// its old supply selector; current fixtures keep compiled requirements.
				var config map[string]json.RawMessage
				if err := json.Unmarshal(data, &config); err != nil {
					return err
				}
				config["dev"] = json.RawMessage(`{"services":{"library":{}}}`)
				data, err = json.Marshal(config)
				if err != nil {
					return err
				}
			}
			if rel == "go.mod" {
				replacement := "/candidate"
				if legacy {
					replacement = "/baseline"
				}
				data = bytes.ReplaceAll(data, []byte("=> ../../.."), []byte("=> "+replacement))
			}
			path := filepath.Join(fixture, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				return err
			}
		}
		if _, err := p.run(dir, "docker", "cp", fixture, s.container+":/app-"+name); err != nil {
			return err
		}
		if _, err := s.cli(legacy, "/app-"+name, "generate", "--target", "typescript_client.public_api"); err != nil {
			return err
		}
	}
	e["binary_provenance"] = provenance
	sourceAfter, err := p.sourceDigest()
	if err != nil {
		return err
	}
	if sourceBefore != sourceAfter {
		return fmt.Errorf("authored source changed while preparing the candidate migration sandbox")
	}
	provenance["candidate_authored_source_sha256"] = sourceBefore
	return nil
}

func (s *worktreeLegacySandbox) up(old bool, root string) (detachedDevResult, error) {
	out, err := s.cli(old, root, "up", "--detach", "--wait", "ready")
	var result detachedDevResult
	if err == nil {
		// Test-only observation of the immutable baseline's result, never a
		// production compatibility reader or control-protocol fallback.
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err = json.Unmarshal(out, &envelope); err == nil {
			err = json.Unmarshal(envelope.Data, &result)
		}
	}
	return result, err
}
