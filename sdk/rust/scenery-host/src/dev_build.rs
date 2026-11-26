use std::collections::BTreeSet;
use std::fs::{File, OpenOptions};
use std::io::{BufRead, BufReader, Read};
use std::os::unix::fs::OpenOptionsExt;
use std::path::{Path, PathBuf};
use std::process::Stdio;

use anyhow::{Context, Result, ensure};
use serde::Deserialize;
use serde_json::Value;

use crate::dev_inputs::{self, extension, under};
use crate::process::{Program, file_digest};

#[derive(Deserialize)]
pub(crate) struct Plan {
    #[serde(default)]
    pub generated: Vec<PathBuf>,
    pub declarations: Option<std::collections::BTreeMap<PathBuf, bool>>,
    pub commands: Vec<Build>,
    pub programs: Vec<Program>,
    pub source_digest: String,
    #[serde(default)]
    pub has_migrations: bool,
    #[serde(default)]
    pub sql_requirements: Option<Box<serde_json::value::RawValue>>,
    #[serde(default)]
    pub js_dependencies: BTreeSet<PathBuf>,
    #[serde(default)]
    pub go_inputs: BTreeSet<PathBuf>,
    pub go_workspace: Option<PathBuf>,
    pub rust_workspace: Option<PathBuf>,
    pub host_config: Value,
}

impl Plan {
    pub(crate) fn explicit_js_dependencies(&self) -> BTreeSet<PathBuf> {
        let mut all = self.js_dependencies.clone();
        for build in &self.commands {
            if build.tool() == "bun" {
                all.extend(build.dependencies.iter().cloned());
            }
        }
        all
    }
    /// Discovers every `//go:embed` file and pattern declared by the Go builds
    /// from the live `inputs` snapshot so globbed and cross-package embeds are
    /// mirrored and tracked before the first rebuild.
    pub(crate) fn discover_go_embeds(
        &mut self,
        root: &Path,
        inputs: &dev_inputs::Inputs,
    ) -> Result<()> {
        for build in &mut self.commands {
            if build.tool() != "go" {
                continue;
            }
            build.embeds.clear();
            build.embed_patterns.clear();
            for path in inputs.keys() {
                if extension(path) == "go" && go_source(path) && build.in_roots(path) {
                    let text = match std::fs::read(root.join(path)) {
                        Ok(text) => text,
                        Err(error) if error.kind() == std::io::ErrorKind::NotFound => continue,
                        Err(error) => return Err(error.into()),
                    };
                    let source = String::from_utf8_lossy(&text);
                    let package_dir = path.parent().unwrap_or_else(|| Path::new(""));
                    for pattern in embed_patterns(&source) {
                        let joined = join_embed_pattern(package_dir, &pattern);
                        if !joined.is_empty() && !build.embed_patterns.contains(&joined) {
                            build.embed_patterns.push(joined);
                        }
                    }
                    for matched in embedded(&source, path, inputs.keys()) {
                        build.embeds.insert(matched.clone());
                        self.go_inputs.insert(matched.clone());
                    }
                }
            }
            build.embeds_discovered = true;
        }
        Ok(())
    }

    /// Reports whether the Go workspace mirrors `path`: Go sources, the
    /// files a build embeds, and everything else inside a package directory
    /// or next to an embedded file, where an embed pattern may select it.
    pub(crate) fn go_mirrored(&self, path: &Path) -> bool {
        !dev_inputs::ignored(path, &self.generated)
            && (go_source(path)
                || self.go_inputs.contains(path)
                || self.beside_embed(path)
                || self.commands.iter().any(|build| {
                    build.tool() == "go" && (build.in_roots(path) || build.matches_embed(path))
                }))
    }

    /// Reports whether `path` sits in, or below, a directory that already
    /// holds an embedded file. Embeds of the root package live in
    /// subdirectories that no package root names.
    fn beside_embed(&self, path: &Path) -> bool {
        self.go_inputs
            .iter()
            .filter(|input| !go_source(input) && !dev_inputs::declaration(input))
            .filter_map(|input| input.parent())
            .any(|directory| {
                if directory.as_os_str().is_empty() {
                    path.parent() == Some(directory)
                } else {
                    path.starts_with(directory)
                }
            })
    }

    fn in_named_go_roots(&self, path: &Path) -> bool {
        self.commands.iter().any(|build| {
            build.tool() == "go"
                && build
                    .roots
                    .iter()
                    .any(|root| root != Path::new(".") && under(path, root))
        })
    }
}

#[derive(Clone, Deserialize)]
pub(crate) struct Build {
    pub process: String,
    pub module: String,
    directory: PathBuf,
    workspace: Option<PathBuf>,
    program: PathBuf,
    args: Vec<String>,
    environment: Option<Vec<String>>,
    #[serde(default)]
    roots: Vec<PathBuf>,
    #[serde(default)]
    owners: Vec<PathBuf>,
    #[serde(default)]
    pub dependencies: BTreeSet<PathBuf>,
    #[serde(default)]
    pub embeds: BTreeSet<PathBuf>,
    #[serde(default)]
    pub embed_patterns: Vec<String>,
    #[serde(default)]
    pub embeds_discovered: bool,
}

impl Build {
    pub(crate) fn tool(&self) -> &str {
        self.program
            .file_name()
            .and_then(|name| name.to_str())
            .unwrap_or("")
    }

    /// Reports whether `path` lies in one of this build's package
    /// directories. The root package owns only the files directly in it.
    fn in_roots(&self, path: &Path) -> bool {
        self.roots.iter().any(|root| {
            if self.tool() == "go" && root == Path::new(".") {
                path.parent() == Some(Path::new(""))
            } else {
                under(path, root)
            }
        })
    }

    fn has_root_package(&self) -> bool {
        self.roots.iter().any(|root| root == Path::new("."))
    }

    /// Reports whether `path` is an embedded input owned by the root package
    /// outside named subpackage roots.
    fn in_root_embeds(&self, plan: &Plan, path: &Path, appeared: bool) -> bool {
        self.has_root_package()
            && !plan.in_named_go_roots(path)
            && (appeared && plan.beside_embed(path)
                || !go_source(path)
                    && !dev_inputs::declaration(path)
                    && plan.go_inputs.contains(path))
    }

    fn matches_embed(&self, path: &Path) -> bool {
        self.embeds.contains(path)
            || self
                .embed_patterns
                .iter()
                .any(|pattern| path_matches_embed_pattern(pattern, path))
    }

    /// Reports whether a change of `path` requires this build to run again.
    /// `appeared` is set for a file that was added or removed: such a file
    /// can change what an existing `//go:embed` pattern selects even though
    /// no build has ever named it.
    pub(crate) fn affected(&self, plan: &Plan, path: &Path, appeared: bool) -> bool {
        match self.tool() {
            "go" if !dev_inputs::ignored(path, &plan.generated) => {
                go_source(path) && self.in_roots(path)
                    || appeared && self.in_roots(path)
                    || !go_source(path)
                        && !dev_inputs::declaration(path)
                        && self.matches_embed(path)
                    || !self.embeds_discovered
                        && (plan.go_inputs.contains(path) && self.in_roots(path)
                            || self.in_root_embeds(plan, path, appeared))
                    || matches!(
                        path.to_str(),
                        Some("go.mod" | "go.sum" | "go.work" | "go.work.sum")
                    )
            }
            "cargo"
                if !dev_inputs::ignored(path, &plan.generated)
                    && !matches!(extension(path), "go" | "scn") =>
            {
                let owner = self
                    .owners
                    .iter()
                    .filter(|owner| under(path, owner))
                    .max_by_key(|owner| owner.components().count());
                owner.map_or_else(
                    || {
                        self.workspace
                            .as_ref()
                            .is_some_and(|root| under(path, root))
                            && matches!(extension(path), "rs" | "toml" | "lock")
                    },
                    |owner| self.roots.contains(owner),
                )
            }
            "bun" if !matches!(extension(path), "go" | "rs" | "scn") => {
                self.dependencies.contains(path)
                    || (!dev_inputs::ignored(path, &plan.generated)
                        && (under(path, &self.directory)
                            || (dev_inputs::declaration(path)
                                && self
                                    .workspace
                                    .as_ref()
                                    .is_some_and(|root| under(path, root))
                                && path
                                    .parent()
                                    .is_some_and(|dir| self.directory.starts_with(dir)))))
            }
            _ => false,
        }
    }

    pub(crate) async fn compile(
        &self,
        root: &Path,
        directory: &Path,
        generated: &[PathBuf],
        previous: &Program,
        revision: &str,
    ) -> Result<(Program, Option<BTreeSet<PathBuf>>)> {
        let staging = tempfile::tempdir_in(directory)?;
        let output = staging.path().join("worker");
        let metafile = staging.path().join("inputs.json");
        let args: Vec<_> = self
            .args
            .iter()
            .map(|arg| {
                arg.replace("{revision}", revision)
                    .replace("{output}", &output.to_string_lossy())
                    .replace("{metafile}", &metafile.to_string_lossy())
            })
            .collect();
        let cargo = self.tool() == "cargo";
        let cwd = if self.tool() == "bun" {
            staging.path()
        } else {
            &self.directory
        };
        let logs = run(
            &self.program,
            &args,
            cwd,
            self.environment.as_deref(),
            staging.path(),
        )
        .await?;
        let root = root.to_owned();
        let cwd = cwd.to_owned();
        let module = root.join(&self.directory);
        let generated = generated.to_vec();
        let target = cargo
            .then(|| self.cargo_target().map(Path::to_owned))
            .transpose()?;
        let bun = self.tool() == "bun";
        let mut next = previous.clone();
        let directory = directory.to_owned();
        let revision = revision.to_owned();
        tokio::task::spawn_blocking(move || {
            let mut bun_dependencies = None;
            let executable = if let Some(target) = target {
                cargo_executable(logs, &target)?
            } else {
                if bun {
                    bun_dependencies =
                        Some(verify_imports(&root, &module, &cwd, &metafile, &generated)?);
                }
                output
            };
            let digest = file_digest(&executable)?;
            if digest != next.digest {
                let artifact = tempfile::Builder::new()
                    .prefix("worker-")
                    .tempfile_in(directory)?;
                std::fs::copy(executable, artifact.path())?;
                next.binary = artifact.path().into();
                next.artifact = Some(std::sync::Arc::new(artifact.into_temp_path()));
                next.digest = digest.clone();
                let identity = if next.runtime == "go" {
                    revision
                } else {
                    digest
                };
                next.identity.implementation_revision = identity.clone();
                next.identity.build_input_digest = identity;
            }
            Ok((next, bun_dependencies))
        })
        .await?
    }

    pub(crate) async fn refresh_go_roots(&mut self, directory: &Path) -> Result<BTreeSet<PathBuf>> {
        // The build command ends with -o, {output}, and its entrypoint.
        let (_, args) = self
            .args
            .split_first()
            .context("missing Go build command")?;
        let (flags, [output, target, entry]) = args
            .split_last_chunk::<3>()
            .context("missing Go build entrypoint")?;
        ensure!(
            output == "-o" && target == "{output}",
            "invalid Go build command"
        );
        let mut list = vec![
            "list".into(),
            "-deps".into(),
            "-json=Dir,EmbedFiles,EmbedPatterns".into(),
        ];
        list.extend(flags.iter().cloned());
        list.push(entry.clone());
        let output = run(
            &self.program,
            &list,
            &self.directory,
            self.environment.as_deref(),
            directory,
        )
        .await?;
        #[derive(Deserialize)]
        #[serde(rename_all = "PascalCase")]
        struct Dependency {
            dir: PathBuf,
            #[serde(default)]
            embed_files: Vec<PathBuf>,
            #[serde(default)]
            embed_patterns: Vec<String>,
        }
        let mut roots = BTreeSet::new();
        let mut embeds = BTreeSet::new();
        let mut patterns = Vec::new();
        for package in
            serde_json::Deserializer::from_reader(BufReader::new(output)).into_iter::<Dependency>()
        {
            let package = package?;
            if let Ok(relative) = package.dir.strip_prefix(&self.directory) {
                let relative = if relative.as_os_str().is_empty() {
                    Path::new(".")
                } else {
                    relative
                };
                roots.insert(relative.to_owned());
                let pattern_base = if relative == Path::new(".") {
                    Path::new("")
                } else {
                    relative
                };
                for pattern in package.embed_patterns {
                    let joined = join_embed_pattern(pattern_base, &pattern);
                    if !joined.is_empty() && !patterns.contains(&joined) {
                        patterns.push(joined);
                    }
                }
                for file in package.embed_files {
                    let path = relative.join(file);
                    // The input scanner records workspace-relative paths.
                    embeds.insert(
                        path.components()
                            .filter(|part| *part != std::path::Component::CurDir)
                            .collect(),
                    );
                }
            }
        }
        self.roots = roots.into_iter().collect();
        self.embeds = embeds.clone();
        self.embed_patterns = patterns;
        self.embeds_discovered = true;
        Ok(embeds)
    }

    pub(crate) fn cargo_lock(&self) -> Option<PathBuf> {
        self.cargo_target()
            .ok()?
            .parent()
            .map(|root| root.join("substrate-cargo-target.lock"))
    }

    fn cargo_target(&self) -> Result<&Path> {
        self.environment
            .iter()
            .flatten()
            .find_map(|entry| entry.strip_prefix("CARGO_TARGET_DIR="))
            .map(Path::new)
            .context("Cargo build is missing its target directory")
    }
}

pub(crate) fn go_source(path: &Path) -> bool {
    matches!(
        extension(path),
        "go" | "c"
            | "cc"
            | "cpp"
            | "cxx"
            | "h"
            | "hh"
            | "hpp"
            | "hxx"
            | "f"
            | "F"
            | "for"
            | "f90"
            | "m"
            | "mm"
            | "s"
            | "S"
            | "syso"
            | "swig"
            | "swigcxx"
    ) && !path.to_string_lossy().ends_with("_test.go")
}

/// Lists the inputs that the `//go:embed` directives of the Go source at
/// `path` select, as `internal/build` selects them for the first workspace.
/// A pattern names a file, or a directory whose whole tree is embedded.
pub(crate) fn embedded<'a>(
    source: &str,
    path: &Path,
    inputs: impl Iterator<Item = &'a PathBuf>,
) -> Vec<&'a PathBuf> {
    let patterns = embed_patterns(source);
    if patterns.is_empty() {
        return Vec::new();
    }
    let package = path.parent().unwrap_or(Path::new(""));
    inputs
        .filter(|input| {
            input.strip_prefix(package).is_ok_and(|file| {
                patterns.iter().any(|pattern| {
                    let mut names = file.iter();
                    pattern.split('/').all(|segment| {
                        names
                            .next()
                            .is_some_and(|name| segment_matches(segment, &name.to_string_lossy()))
                    })
                })
            })
        })
        .collect()
}

fn join_embed_pattern(dir: &Path, raw: &str) -> String {
    let raw = raw
        .trim()
        .strip_prefix("all:")
        .unwrap_or(raw.trim())
        .trim_start_matches("./");
    if raw.is_empty() {
        return String::new();
    }
    if dir.as_os_str().is_empty() || dir == Path::new(".") {
        raw.to_owned()
    } else {
        format!("{}/{}", dir.to_string_lossy().trim_end_matches('/'), raw)
    }
}

fn path_matches_embed_pattern(pattern: &str, path: &Path) -> bool {
    let pattern = pattern.trim();
    if pattern.is_empty() {
        return false;
    }
    let mut names = path.iter();
    pattern.split('/').all(|segment| {
        names
            .next()
            .is_some_and(|name| segment_matches(segment, &name.to_string_lossy()))
    })
}

/// Reads the patterns of every `//go:embed` line: bare words and Go string
/// literals, without the `all:` prefix that only widens a directory.
fn embed_patterns(source: &str) -> Vec<String> {
    let mut patterns = Vec::new();
    for line in source.lines() {
        let Some(mut rest) = line.trim().strip_prefix("//go:embed") else {
            continue;
        };
        loop {
            rest = rest.trim_start();
            let (pattern, next) = match rest.chars().next() {
                None => break,
                Some('`') => match rest[1..].split_once('`') {
                    Some((raw, next)) => (raw.to_owned(), next),
                    None => break,
                },
                Some('"') => {
                    let mut escaped = false;
                    let end = rest[1..].find(|c| {
                        let closes = c == '"' && !escaped;
                        escaped = c == '\\' && !escaped;
                        closes
                    });
                    let Some(end) = end else { break };
                    // The escapes of a file name are the ones JSON shares.
                    let Ok(text) = serde_json::from_str(&rest[..end + 2]) else {
                        break;
                    };
                    (text, &rest[end + 2..])
                }
                Some(_) => {
                    let end = rest.find(char::is_whitespace).unwrap_or(rest.len());
                    (rest[..end].to_owned(), &rest[end..])
                }
            };
            patterns.push(pattern.strip_prefix("all:").unwrap_or(&pattern).to_owned());
            rest = next;
        }
    }
    patterns
}

/// Matches one path segment against a segment of a `path.Match` pattern. A
/// character class or an escape matches anything: a file the build does not
/// embed is harmless in the workspace, a missing one fails the build.
fn segment_matches(pattern: &str, name: &str) -> bool {
    let mut rest = pattern.chars();
    match rest.next() {
        None => name.is_empty(),
        Some('[' | '\\') => true,
        Some('*') => name
            .char_indices()
            .map(|(at, _)| at)
            .chain([name.len()])
            .any(|at| segment_matches(rest.as_str(), &name[at..])),
        Some(expected) => {
            let mut name = name.chars();
            name.next()
                .is_some_and(|actual| expected == '?' || expected == actual)
                && segment_matches(rest.as_str(), name.as_str())
        }
    }
}

pub(crate) async fn lock_file(path: &Path) -> Result<File> {
    use std::os::fd::AsRawFd;
    let file = OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .mode(0o644)
        .open(path)?;
    loop {
        if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } == 0 {
            return Ok(file);
        }
        let error = std::io::Error::last_os_error();
        if error.kind() != std::io::ErrorKind::WouldBlock {
            return Err(error.into());
        }
        tokio::time::sleep(std::time::Duration::from_millis(10)).await;
    }
}

struct Compiler(tokio::process::Child, u32);

impl Drop for Compiler {
    fn drop(&mut self) {
        if self.1 != 0 {
            unsafe { libc::kill(-(self.1 as libc::pid_t), libc::SIGKILL) };
        }
    }
}

pub(crate) async fn run(
    program: &Path,
    args: &[String],
    cwd: &Path,
    environment: Option<&[String]>,
    staging: &Path,
) -> Result<File> {
    let stdout = tempfile::tempfile_in(staging)?;
    let stderr = tempfile::tempfile_in(staging)?;
    let mut command = tokio::process::Command::new(program);
    command
        .args(args)
        .current_dir(cwd)
        .stdin(Stdio::null())
        .stdout(stdout.try_clone()?)
        .stderr(stderr.try_clone()?)
        .process_group(0)
        .kill_on_drop(true);
    if let Some(environment) = environment {
        command
            .env_clear()
            .envs(environment.iter().filter_map(|entry| entry.split_once('=')));
    }
    let child = command.spawn()?;
    let pid = child.id().context("compiler did not start")?;
    let mut child = Compiler(child, pid);
    let status = child.0.wait().await?;
    child.1 = 0;
    use std::io::{Seek, SeekFrom};
    let mut stdout = stdout;
    stdout.seek(SeekFrom::Start(0))?;
    if !status.success() {
        let mut stderr = stderr;
        stderr.seek(SeekFrom::Start(0))?;
        let mut diagnostic = Vec::new();
        stderr.take(12 << 10).read_to_end(&mut diagnostic)?;
        if diagnostic.is_empty() {
            stdout.take(12 << 10).read_to_end(&mut diagnostic)?;
        }
        anyhow::bail!("{}", String::from_utf8_lossy(&diagnostic).trim());
    }
    Ok(stdout)
}

fn cargo_executable(stdout: File, target: &Path) -> Result<PathBuf> {
    for line in BufReader::new(stdout).lines() {
        let entry: Value = serde_json::from_str(&line?)?;
        if entry["reason"] == "compiler-artifact"
            && let Some(path) = entry["executable"].as_str()
        {
            let path = PathBuf::from(path);
            ensure!(
                path.canonicalize()?.starts_with(target.canonicalize()?),
                "Cargo returned an executable outside its target directory"
            );
            return Ok(path);
        }
    }
    anyhow::bail!("Cargo did not report the worker executable")
}

fn verify_imports(
    root: &Path,
    module: &Path,
    cwd: &Path,
    metafile: &Path,
    generated: &[PathBuf],
) -> Result<BTreeSet<PathBuf>> {
    let graph: Value = serde_json::from_reader(File::open(metafile)?)?;
    let inputs = graph["inputs"]
        .as_object()
        .filter(|inputs| !inputs.is_empty())
        .context("Bun did not report its worker inputs")?;
    let dependency = |path: &Path| {
        path.strip_prefix(root)
            .is_ok_and(|relative| relative.iter().any(|part| part == "node_modules"))
    };
    let mut tracked = BTreeSet::new();
    for path in inputs.keys() {
        let path = std::path::absolute(cwd.join(path))?;
        // Bun's metafile can use paths relative to a staging directory.
        let path = path.components().fold(PathBuf::new(), |mut path, part| {
            if part == std::path::Component::ParentDir {
                path.pop();
            } else {
                path.push(part);
            }
            path
        });
        // Bun can spell Darwin's system aliases without the /private
        // prefix. Keep the ordinary symlink check for every app component.
        #[cfg(target_os = "macos")]
        let path = [Path::new("/var"), Path::new("/tmp")]
            .iter()
            .find_map(|alias| {
                path.strip_prefix(alias)
                    .ok()
                    .and_then(|tail| alias.canonicalize().ok().map(|prefix| prefix.join(tail)))
            })
            .unwrap_or(path);
        let resolved = path.canonicalize()?;
        ensure!(
            path.starts_with(module) && path == resolved
                || dependency(&path) && dependency(&resolved),
            "JavaScript worker imports must stay in its module or installed workspace dependencies: {}",
            path.display()
        );
        if let Ok(rel) = path.strip_prefix(root)
            && !dev_inputs::always_ignored(rel, generated)
        {
            tracked.insert(rel.to_owned());
        }
    }
    Ok(tracked)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[tokio::test]
    async fn rebuild_watches_a_new_local_go_import() -> Result<()> {
        let temp = tempfile::tempdir()?;
        let root = temp.path().canonicalize()?;
        let workspace = root.join("source");
        let artifacts = root.join("artifacts");
        std::fs::create_dir_all(workspace.join("worker"))?;
        std::fs::create_dir_all(workspace.join("extra"))?;
        std::fs::create_dir(&artifacts)?;
        std::fs::write(
            workspace.join("go.mod"),
            "module example.test/app\n\ngo 1.23\n",
        )?;
        std::fs::write(
            workspace.join("worker/main.go"),
            "package main\nfunc main() {}\n",
        )?;
        let extra = Path::new("extra/value.go");
        std::fs::write(
            workspace.join(extra),
            "package extra\nconst Value = \"first\"\n",
        )?;
        let mut plan: Plan = serde_json::from_value(json!({
            "commands":[{
                "process":"worker", "module":"worker", "directory":workspace, "program":"go",
                "args":["build","-o","{output}","./worker"], "roots":["worker"],
            }],
            "programs":[], "source_digest":"", "host_config":{},
        }))?;
        let initial: Program = serde_json::from_value(json!({
            "service":"app/service/worker","process":"worker","binary":"","digest":"","runtime":"go",
            "identity":{"contract_revision":"contract","implementation_revision":"","build_input_digest":"","go_target":"test"},
            "environment":[],
        }))?;
        assert!(!plan.commands[0].affected(&plan, extra, false));
        let (original, _) = plan.commands[0]
            .compile(&root, &artifacts, &[], &initial, "initial")
            .await?;
        std::fs::write(
            workspace.join("worker/main.go"),
            "package main\nimport \"example.test/app/extra\"\nfunc main() { println(extra.Value) }\n",
        )?;
        let command = &mut plan.commands[0];
        let (first, _) = command
            .compile(&root, &artifacts, &[], &original, "import")
            .await?;
        command.refresh_go_roots(&artifacts).await?;
        assert_ne!(original.digest, first.digest);
        assert!(plan.commands[0].affected(&plan, extra, false));

        std::fs::write(
            workspace.join(extra),
            "package extra\nconst Value = \"second\"\n",
        )?;
        let (second, _) = plan.commands[0]
            .compile(&root, &artifacts, &[], &first, "dependency edit")
            .await?;
        assert_ne!(first.digest, second.digest);
        Ok(())
    }

    #[test]
    fn embed_directives_select_files_globs_and_directory_trees() {
        let source = "package web\n\n//go:embed static/*.css \"my file.txt\" `raw name`\n\t//go:embed all:templates\nvar files embed.FS\n// go:embed ignored.txt\n";
        assert_eq!(
            embed_patterns(source),
            ["static/*.css", "my file.txt", "raw name", "templates"]
        );
        let inputs = [
            "web/static/app.css",
            "web/static/app.js",
            "web/static/deep/app.css",
            "web/my file.txt",
            "web/templates/mail/_hidden.txt",
            "web/ignored.txt",
            "other/static/app.css",
            "static/app.css",
        ]
        .map(PathBuf::from);
        let selected: Vec<_> = embedded(source, Path::new("web/handler.go"), inputs.iter())
            .into_iter()
            .map(|path| path.to_str().unwrap())
            .collect();
        assert_eq!(
            selected,
            [
                "web/static/app.css",
                "web/my file.txt",
                "web/templates/mail/_hidden.txt"
            ]
        );
    }

    #[test]
    fn a_new_file_under_an_embed_pattern_reaches_the_go_build() -> Result<()> {
        let plan: Plan = serde_json::from_value(json!({
            "commands":[
                {"process":"web","module":"web","directory":"/workspace","program":"go",
                 "args":[],"roots":["web"]},
                {"process":"root","module":"root","directory":"/workspace","program":"go",
                 "args":[],"roots":["."]},
                {"process":"pricing","module":"pricing","directory":"/app/pricing","program":"cargo",
                 "args":[],"roots":["pricing"],"owners":["pricing"]},
            ],
            "programs":[], "source_digest":"", "host_config":{},
            "go_inputs":["web/static/app.css","web/handler.go","templates/mail/hello.txt","main.go"],
        }))?;
        let (web, root, cargo) = (&plan.commands[0], &plan.commands[1], &plan.commands[2]);
        // `//go:embed static/*` already exists: a new file it matches must be
        // mirrored and rebuild the worker, although no build ever named it.
        let added = Path::new("web/static/new.txt");
        assert!(plan.go_mirrored(added));
        assert!(web.affected(&plan, added, true));
        assert!(!cargo.affected(&plan, added, true));
        // Editing a file no pattern selected yet changes nothing.
        assert!(!web.affected(&plan, Path::new("web/static/notes.md"), false));
        assert!(web.affected(&plan, Path::new("web/static/app.css"), false));
        // The root package embeds from subdirectories no package root names.
        let nested = Path::new("templates/mail/welcome.txt");
        assert!(plan.go_mirrored(nested));
        assert!(root.affected(&plan, nested, true));
        assert!(!web.affected(&plan, nested, true));
        assert!(root.affected(&plan, Path::new("templates/mail/sub/new.txt"), true));
        // Editing an already-known embedded file outside package roots must
        // rebuild the root worker without rebuilding unrelated subpackages.
        let existing = Path::new("templates/mail/hello.txt");
        assert!(root.affected(&plan, existing, false));
        assert!(!web.affected(&plan, existing, false));
        // An unrelated file is neither mirrored nor a reason to rebuild.
        let unrelated = Path::new("docs/guide.md");
        assert!(!plan.go_mirrored(unrelated));
        assert!(!web.affected(&plan, unrelated, true) && !root.affected(&plan, unrelated, true));
        Ok(())
    }

    #[test]
    fn globbed_and_cross_package_go_embeds_trigger_mirroring_and_rebuilds() -> Result<()> {
        let temp = tempfile::tempdir()?;
        let root_dir = temp.path().canonicalize()?;
        std::fs::create_dir_all(root_dir.join("templates/en"))?;
        std::fs::create_dir_all(root_dir.join("web"))?;
        std::fs::write(
            root_dir.join("main.go"),
            "package main\n//go:embed templates/*/mail.html web/shared.json\nvar assets embed.FS\n",
        )?;
        std::fs::write(root_dir.join("templates/en/mail.html"), "hi")?;
        std::fs::write(root_dir.join("web/shared.json"), "{}")?;
        std::fs::write(
            root_dir.join("web/handler.go"),
            "package web\n//go:embed static.txt\nvar s string\n",
        )?;
        std::fs::write(root_dir.join("web/static.txt"), "ok")?;

        let scanner = dev_inputs::Scanner::new(root_dir.clone());
        let initial = scanner.scan(&[])?;
        let mut plan: Plan = serde_json::from_value(json!({
            "commands":[
                {"process":"root","module":"root","directory":root_dir,"program":"go","args":[],"roots":["."]},
                {"process":"web","module":"web","directory":root_dir,"program":"go","args":[],"roots":["web"]},
            ],
            "programs":[], "source_digest":"", "host_config":{},
        }))?;
        plan.discover_go_embeds(&root_dir, &initial)?;

        // Adding a sibling locale under `templates/*/mail.html` matches the discovered glob.
        let new_locale = Path::new("templates/cs/mail.html");
        assert!(plan.go_mirrored(new_locale));
        assert!(plan.commands[0].affected(&plan, new_locale, true));
        assert!(!plan.commands[1].affected(&plan, new_locale, true));

        // Editing a file inside `web/` embedded only by `root` rebuilds `root`, not `web`.
        let shared = Path::new("web/shared.json");
        assert!(plan.commands[0].affected(&plan, shared, false));
        assert!(!plan.commands[1].affected(&plan, shared, false));
        Ok(())
    }

    #[test]
    fn bun_builds_track_explicit_dependencies_under_ignored_dirs() -> Result<()> {
        let plan: Plan = serde_json::from_value(json!({
            "commands": [{
                "process": "jsw",
                "module": "svc",
                "directory": "svc",
                "workspace": ".",
                "program": "bun",
                "args": [],
                "dependencies": ["svc/dist/vendor.ts", "node_modules/pkg/index.js"]
            }],
            "programs": [],
            "source_digest": "",
            "host_config": {},
        }))?;
        let bun = &plan.commands[0];
        assert!(bun.affected(&plan, Path::new("svc/dist/vendor.ts"), false));
        assert!(bun.affected(&plan, Path::new("node_modules/pkg/index.js"), false));
        assert!(!bun.affected(&plan, Path::new("node_modules/other/index.js"), false));
        assert_eq!(
            plan.explicit_js_dependencies(),
            BTreeSet::from([
                PathBuf::from("svc/dist/vendor.ts"),
                PathBuf::from("node_modules/pkg/index.js"),
            ])
        );
        Ok(())
    }
}
