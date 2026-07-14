package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/deployplan"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/evolution"
	"scenery.sh/internal/toolchain"
)

const (
	upgradeKind = "scenery.upgrade"
)

var (
	upgradeAPIBaseURL       = "https://api.github.com/repos/scenery-sh/scenery"
	upgradeHTTPClient       = http.DefaultClient
	upgradeCommandContext   = exec.CommandContext
	upgradeCurrentVersionFn = func() string { return buildVersionResponse().Version }
	upgradeDeployNoticeFunc = defaultUpgradeDeployNotice
	upgradeWorkingDirectory = os.Getwd
)

type upgradeOptions struct {
	JSON          bool
	Target        string
	DryRun        bool
	Force         bool
	ToolchainMode string
}

type upgradeResponse struct {
	cliPayloadIdentity
	OK             bool                    `json:"ok"`
	Repository     string                  `json:"repository"`
	Platform       string                  `json:"platform"`
	CurrentVersion string                  `json:"current_version"`
	TargetVersion  string                  `json:"target_version"`
	TargetPath     string                  `json:"target_path"`
	AssetName      string                  `json:"asset_name"`
	ChecksumSHA256 string                  `json:"checksum_sha256,omitempty"`
	Installed      bool                    `json:"installed"`
	Skipped        bool                    `json:"skipped"`
	DryRun         bool                    `json:"dry_run"`
	Reason         string                  `json:"reason,omitempty"`
	Toolchain      *upgradeToolchainResult `json:"toolchain,omitempty"`
	Deploy         *deployHelperDrift      `json:"deploy,omitempty"`
	Error          string                  `json:"error,omitempty"`
}

type upgradeToolchainResult struct {
	Mode     string                 `json:"mode"`
	StoreDir string                 `json:"store_dir,omitempty"`
	Synced   []upgradeToolchainSync `json:"synced,omitempty"`
	Skipped  []upgradeToolchainSync `json:"skipped,omitempty"`
}

type upgradeToolchainSync struct {
	Tool    string `json:"tool"`
	Kind    string `json:"kind,omitempty"`
	Images  bool   `json:"images,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type upgradeRelease struct {
	TagName    string                 `json:"tag_name"`
	Draft      bool                   `json:"draft"`
	Prerelease bool                   `json:"prerelease"`
	Assets     []upgradeReleaseAsset  `json:"assets"`
	Extra      map[string]interface{} `json:"-"`
}

type upgradeReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type upgradeToolchainCandidate struct {
	Name   string
	Kind   string
	Images bool
}

func upgradeCommand(args []string) error {
	return runUpgrade(context.Background(), os.Stdout, args)
}

func runUpgrade(ctx context.Context, stdout io.Writer, args []string) error {
	if len(args) == 1 && args[0] == "--help" {
		entry, _ := findHelpCommand([]string{"upgrade"})
		writeCommandHelp(stdout, entry)
		return nil
	}
	opts, err := parseUpgradeArgs(args)
	if err != nil {
		return err
	}
	resp, err := performUpgrade(ctx, opts)
	if opts.JSON {
		if encodeErr := writeCLIJSON(stdout, resp); encodeErr != nil {
			return encodeErr
		}
		if err != nil {
			return &silentCLIError{err: err}
		}
		return nil
	}
	if err != nil {
		return err
	}
	return renderUpgrade(stdout, resp)
}

func parseUpgradeArgs(args []string) (upgradeOptions, error) {
	opts := upgradeOptions{ToolchainMode: "installed"}
	skipToolchain := false
	flags := newCLIFlagSet("upgrade")
	registerJSONOutput(flags, &opts.JSON)
	flags.BoolVar(&opts.DryRun, "dry-run", false, "")
	flags.BoolVar(&opts.Force, "force", false, "")
	flags.BoolVar(&skipToolchain, "skip-toolchain", false, "")
	flags.StringVar(&opts.Target, "target", "", "")
	flags.StringVar(&opts.ToolchainMode, "toolchain", opts.ToolchainMode, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return upgradeOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return upgradeOptions{}, err
	}
	if skipToolchain {
		opts.ToolchainMode = "none"
	}
	opts.ToolchainMode = strings.TrimSpace(opts.ToolchainMode)
	switch opts.ToolchainMode {
	case "installed", "all", "none":
	default:
		return upgradeOptions{}, fmt.Errorf("--toolchain must be installed, all, or none")
	}
	return opts, nil
}

func performUpgrade(ctx context.Context, opts upgradeOptions) (upgradeResponse, error) {
	defaultTarget := strings.TrimSpace(opts.Target) == ""
	target, err := upgradeTargetPath(opts.Target)
	resp := upgradeResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(upgradeKind),
		Repository:         "scenery-sh/scenery",
		Platform:           toolchain.CurrentPlatform().String(),
		CurrentVersion:     upgradeCurrentVersionFn(),
		TargetPath:         target,
		DryRun:             opts.DryRun,
	}
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}
	cwd, err := upgradeWorkingDirectory()
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}
	storeDir := filepath.Clean(toolchain.DefaultStoreDir(cwd))
	var candidates []upgradeToolchainCandidate
	if opts.ToolchainMode == "installed" {
		var collectErr error
		candidates, storeDir, collectErr = collectInstalledToolchainCandidates(ctx, cwd)
		if collectErr != nil {
			resp.Error = collectErr.Error()
			return resp, collectErr
		}
	}
	release, err := fetchUpgradeRelease(ctx)
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}
	resp.TargetVersion = strings.TrimSpace(release.TagName)
	assetName := upgradeArchiveName(resp.TargetVersion)
	resp.AssetName = assetName
	asset, ok := findUpgradeAsset(release, assetName)
	if !ok {
		err := fmt.Errorf("release %s does not include asset %s", resp.TargetVersion, assetName)
		resp.Error = err.Error()
		return resp, err
	}
	checksums, ok := findUpgradeAsset(release, "checksums.txt")
	if !ok {
		err := fmt.Errorf("release %s does not include checksums.txt", resp.TargetVersion)
		resp.Error = err.Error()
		return resp, err
	}
	if sameVersion(resp.CurrentVersion, resp.TargetVersion) && !opts.Force && defaultTarget {
		resp.OK = true
		resp.Skipped = true
		resp.Reason = "already current"
		toolchainResult, err := runUpgradeToolchainSync(ctx, target, cwd, opts.ToolchainMode, storeDir, candidates, opts.DryRun)
		resp.Toolchain = toolchainResult
		if err != nil {
			resp.OK = false
			resp.Error = err.Error()
			return resp, err
		}
		attachUpgradeDeployNotice(&resp)
		return resp, nil
	}
	if opts.DryRun {
		resp.OK = true
		resp.Reason = "dry run"
		resp.Toolchain = &upgradeToolchainResult{Mode: opts.ToolchainMode, StoreDir: storeDir}
		return resp, nil
	}
	sumData, err := downloadUpgradeAsset(ctx, checksums)
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}
	want, ok := parseUpgradeChecksums(sumData)[assetName]
	if !ok {
		err := fmt.Errorf("checksums.txt does not include %s", assetName)
		resp.Error = err.Error()
		return resp, err
	}
	archiveData, err := downloadUpgradeAsset(ctx, asset)
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}
	if err := verifyUpgradeChecksum(archiveData, want); err != nil {
		resp.Error = err.Error()
		return resp, err
	}
	resp.ChecksumSHA256 = strings.ToLower(want)
	binaryData, err := extractUpgradeBinary(archiveData, assetName)
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}
	if err := preflightUpgradeRecoveryState(cwd); err != nil {
		resp.Error = err.Error()
		return resp, err
	}
	if err := installUpgradeBinary(target, binaryData); err != nil {
		resp.Error = err.Error()
		return resp, err
	}
	resp.Installed = true
	toolchainResult, err := runUpgradeToolchainSync(ctx, target, cwd, opts.ToolchainMode, storeDir, candidates, false)
	resp.Toolchain = toolchainResult
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}
	resp.OK = true
	attachUpgradeDeployNotice(&resp)
	return resp, nil
}

func preflightUpgradeRecoveryState(cwd string) error {
	root, _, err := appcfg.DiscoverRoot(cwd)
	if errors.Is(err, appcfg.ErrRootNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed_precondition: inspect the current app before replacing Scenery: %w", err)
	}
	return checkLegacyRecoveryState(root)
}

func checkLegacyRecoveryState(root string) error {
	if err := evolution.CheckLegacyRecoveryState(root); err != nil {
		return err
	}
	return deployplan.CheckLegacyRecoveryState(root)
}

func attachUpgradeDeployNotice(resp *upgradeResponse) {
	if resp == nil || !resp.OK || resp.DryRun {
		return
	}
	resp.Deploy = upgradeDeployNoticeFunc(resp.TargetVersion)
}

func defaultUpgradeDeployNotice(targetVersion string) *deployHelperDrift {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return nil
	}
	helper := privilegedListenerStatus(paths)
	if !helper.Installed {
		return nil
	}
	drift := deployHelperDriftFor(helper, targetVersion)
	if !drift.ActionRequired {
		return nil
	}
	return &drift
}

func upgradeTargetPath(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return filepath.Abs(explicit)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Abs(exe)
}

func fetchUpgradeRelease(ctx context.Context) (upgradeRelease, error) {
	endpoint := strings.TrimRight(upgradeAPIBaseURL, "/") + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return upgradeRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "scenery-upgrade")
	client := upgradeHTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return upgradeRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return upgradeRelease{}, fmt.Errorf("fetch release metadata: unexpected status %s", resp.Status)
	}
	var release upgradeRelease
	dec := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
	if err := dec.Decode(&release); err != nil {
		return upgradeRelease{}, err
	}
	if strings.TrimSpace(release.TagName) == "" {
		return upgradeRelease{}, fmt.Errorf("release metadata did not include tag_name")
	}
	if release.Draft {
		return upgradeRelease{}, fmt.Errorf("release %s is a draft", release.TagName)
	}
	return release, nil
}

func upgradeArchiveName(tag string) string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	version := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	return fmt.Sprintf("scenery_%s_%s_%s.%s", version, runtime.GOOS, runtime.GOARCH, ext)
}

func findUpgradeAsset(release upgradeRelease, name string) (upgradeReleaseAsset, bool) {
	for _, asset := range release.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return upgradeReleaseAsset{}, false
}

func downloadUpgradeAsset(ctx context.Context, asset upgradeReleaseAsset) ([]byte, error) {
	if strings.TrimSpace(asset.BrowserDownloadURL) == "" {
		return nil, fmt.Errorf("asset %s does not include browser_download_url", asset.Name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "scenery-upgrade")
	client := upgradeHTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected status %s", asset.Name, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, err
	}
	if len(data) == 512<<20 {
		return nil, fmt.Errorf("download %s exceeded size limit", asset.Name)
	}
	return data, nil
}

func parseUpgradeChecksums(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || !isSHA256Hex(fields[0]) {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		out[name] = strings.ToLower(fields[0])
	}
	return out
}

func verifyUpgradeChecksum(data []byte, want string) error {
	want = strings.ToLower(strings.TrimSpace(want))
	if !isSHA256Hex(want) {
		return fmt.Errorf("invalid checksum")
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("checksum mismatch: got %s want %s", got, want)
	}
	return nil
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func extractUpgradeBinary(data []byte, archiveName string) ([]byte, error) {
	want := "scenery"
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if strings.HasSuffix(archiveName, ".zip") {
		return extractUpgradeBinaryFromZip(data, want)
	}
	return extractUpgradeBinaryFromTarGz(data, want)
}

func extractUpgradeBinaryFromTarGz(data []byte, want string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != want {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, 256<<20))
	}
	return nil, fmt.Errorf("archive did not contain %s", want)
}

func extractUpgradeBinaryFromZip(data []byte, want string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, file := range zr.File {
		if filepath.Base(file.Name) != want {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		out, readErr := io.ReadAll(io.LimitReader(rc, 256<<20))
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return out, nil
	}
	return nil, fmt.Errorf("archive did not contain %s", want)
}

func installUpgradeBinary(target string, binary []byte) error {
	if len(binary) == 0 {
		return fmt.Errorf("downloaded binary is empty")
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".scenery-upgrade-"+fmt.Sprint(os.Getpid())+"-"+time.Now().UTC().Format("20060102150405.000000000"))
	if err := os.WriteFile(tmp, binary, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func collectInstalledToolchainCandidates(ctx context.Context, cwd string) ([]upgradeToolchainCandidate, string, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return nil, "", err
	}
	store, err := toolchain.NewStore(toolchain.DefaultStoreDir(cwd), manifest)
	if err != nil {
		return nil, "", err
	}
	store.RootDir = cwd
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	status, err := store.Verify(ctx, toolchain.Options{RootDir: cwd, Platform: toolchain.CurrentPlatform(), Images: true})
	if err != nil {
		return nil, status.StoreDir, err
	}
	var candidates []upgradeToolchainCandidate
	for _, artifact := range status.Artifacts {
		switch artifact.Kind {
		case "binary":
			if artifact.Status == "installed" || artifact.Status == "invalid" {
				candidates = append(candidates, upgradeToolchainCandidate{Name: artifact.Name, Kind: artifact.Kind, Images: hasPresentUpgradeImage(artifact.Images)})
			}
		case "image":
			if hasPresentUpgradeImage(artifact.Images) {
				candidates = append(candidates, upgradeToolchainCandidate{Name: artifact.Name, Kind: artifact.Kind, Images: true})
			}
		}
	}
	return candidates, status.StoreDir, nil
}

func hasPresentUpgradeImage(images []toolchain.ImageStatus) bool {
	for _, image := range images {
		if image.Status == "present" {
			return true
		}
	}
	return false
}

func runUpgradeToolchainSync(ctx context.Context, binaryPath, cwd, mode, storeDir string, candidates []upgradeToolchainCandidate, dryRun bool) (*upgradeToolchainResult, error) {
	result := &upgradeToolchainResult{Mode: mode, StoreDir: storeDir}
	if mode == "none" || dryRun {
		return result, nil
	}
	if mode == "all" {
		item := upgradeToolchainSync{Tool: "*", Status: "synced", Images: true}
		output, err := runUpgradeToolchainCommand(ctx, binaryPath, cwd, []string{"system", "toolchain", "sync", "-o", "json", "--images"})
		if err != nil {
			item.Status = "error"
			item.Message = strings.TrimSpace(output)
			result.Synced = append(result.Synced, item)
			return result, fmt.Errorf("toolchain sync failed: %w: %s", err, strings.TrimSpace(output))
		}
		result.Synced = append(result.Synced, item)
		return result, nil
	}
	for _, candidate := range candidates {
		args := []string{"system", "toolchain", "sync", "-o", "json", "--tool", candidate.Name}
		if candidate.Images {
			args = append(args, "--images")
		}
		item := upgradeToolchainSync{Tool: candidate.Name, Kind: candidate.Kind, Images: candidate.Images, Status: "synced"}
		output, err := runUpgradeToolchainCommand(ctx, binaryPath, cwd, args)
		if err != nil {
			msg := strings.TrimSpace(output)
			item.Message = msg
			if strings.Contains(msg, "unknown toolchain artifact") {
				item.Status = "skipped"
				result.Skipped = append(result.Skipped, item)
				continue
			}
			item.Status = "error"
			result.Synced = append(result.Synced, item)
			return result, fmt.Errorf("toolchain sync %s failed: %w: %s", candidate.Name, err, msg)
		}
		result.Synced = append(result.Synced, item)
	}
	return result, nil
}

func runUpgradeToolchainCommand(ctx context.Context, binaryPath, cwd string, args []string) (string, error) {
	cmd := upgradeCommandContext(ctx, binaryPath, args...)
	cmd.Dir = cwd
	cmd.Env = envpolicy.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func sameVersion(a, b string) bool {
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		value = strings.TrimPrefix(value, "v")
		return value
	}
	return normalize(a) != "" && normalize(a) == normalize(b)
}

func renderUpgrade(stdout io.Writer, resp upgradeResponse) error {
	switch {
	case resp.DryRun:
		fmt.Fprintf(stdout, "would upgrade scenery %s -> %s at %s\n", resp.CurrentVersion, resp.TargetVersion, resp.TargetPath)
	case resp.Skipped:
		fmt.Fprintf(stdout, "scenery is already %s at %s\n", resp.TargetVersion, resp.TargetPath)
	case resp.Installed:
		fmt.Fprintf(stdout, "upgraded scenery %s -> %s at %s\n", resp.CurrentVersion, resp.TargetVersion, resp.TargetPath)
	default:
		fmt.Fprintf(stdout, "scenery upgrade checked %s at %s\n", resp.TargetVersion, resp.TargetPath)
	}
	if resp.Toolchain != nil && resp.Toolchain.Mode != "none" {
		fmt.Fprintf(stdout, "toolchain sync: %s", resp.Toolchain.Mode)
		if len(resp.Toolchain.Synced) > 0 {
			fmt.Fprintf(stdout, ", synced %d", len(resp.Toolchain.Synced))
		}
		if len(resp.Toolchain.Skipped) > 0 {
			fmt.Fprintf(stdout, ", skipped %d", len(resp.Toolchain.Skipped))
		}
		fmt.Fprintln(stdout)
	}
	if resp.Deploy != nil && resp.Deploy.ActionRequired {
		fmt.Fprintf(stdout, "deploy helper: %s\n", resp.Deploy.SuggestedAction)
	}
	return nil
}
