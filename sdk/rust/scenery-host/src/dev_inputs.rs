use std::collections::{BTreeMap, HashMap};
use std::fs;
use std::io;
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::{MetadataExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, SystemTime};

use anyhow::{Result, ensure};
use sha2::{Digest, Sha256};

use crate::gitignore;
use crate::process::{hex, sha256_file};

#[derive(Clone, Eq, PartialEq)]
pub(crate) struct Input {
    digest: [u8; 32],
    mode: u32,
}

pub(crate) type Inputs = BTreeMap<PathBuf, Input>;

pub(crate) fn ignored(path: &Path, generated: &[PathBuf]) -> bool {
    generated.iter().any(|root| under(path, root))
        || path.iter().any(|component| {
            let name = component.to_string_lossy();
            name.starts_with(".env")
                || matches!(
                    name.as_ref(),
                    ".git"
                        | ".scenery"
                        | ".claude"
                        | "node_modules"
                        | "dist"
                        | "out"
                        | "target"
                        | "scenerycontract"
                        | "scenerygen"
                        | "scenery_internal_main"
                        | "scenery_internal_processes"
                        | ".DS_Store"
                )
        })
}

pub(crate) fn under(path: &Path, root: &Path) -> bool {
    root == Path::new(".") || path.starts_with(root)
}

/// What `lstat` reports about a file. An unchanged stamp vouches for the
/// digest computed under it, as the Git index does for its entries.
#[derive(Clone, Copy, Eq, PartialEq)]
struct Stamp {
    device: u64,
    inode: u64,
    size: u64,
    mode: u32,
    modified: (i64, i64),
    changed: (i64, i64),
}

struct Cached {
    stamp: Stamp,
    digest: [u8; 32],
    /// The file changed so close to the scan that a later write could keep
    /// the same timestamps; the next scan reads it again.
    racy: bool,
}

/// Timestamps this close to a scan cannot tell two writes apart on a file
/// system with coarse clocks.
const RACY_WINDOW: Duration = Duration::from_secs(2);

/// Scans the application's development inputs. Every scan walks the tree
/// with `lstat`, but reads only the files whose stamp changed: an unchanged
/// repository costs no hashing, whatever its size.
pub(crate) struct Scanner {
    root: PathBuf,
    cache: Mutex<HashMap<PathBuf, Cached>>,
    hashed: AtomicUsize,
    racy_window: Duration,
}

impl Scanner {
    pub(crate) fn new(root: PathBuf) -> Self {
        Self {
            root,
            cache: Mutex::default(),
            hashed: AtomicUsize::new(0),
            racy_window: RACY_WINDOW,
        }
    }

    pub(crate) fn root(&self) -> &Path {
        &self.root
    }

    /// The number of files read so far.
    #[cfg(test)]
    pub(crate) fn hashed(&self) -> usize {
        self.hashed.load(Ordering::Relaxed)
    }

    /// Blocks on file system work: call it from a blocking thread.
    pub(crate) fn scan(&self, generated: &[PathBuf]) -> Result<Inputs> {
        let mut cache = self.cache.lock().expect("input cache");
        let mut walk = Walk {
            scanner: self,
            generated,
            previous: &cache,
            next: HashMap::with_capacity(cache.len()),
            inputs: Inputs::new(),
            rules: Vec::new(),
            racy_after: SystemTime::now()
                .checked_sub(self.racy_window)
                .unwrap_or(SystemTime::UNIX_EPOCH),
        };
        walk.entry(Path::new(""), &mut Vec::new(), None)?;
        let (next, inputs) = (walk.next, walk.inputs);
        *cache = next;
        Ok(inputs)
    }
}

struct Walk<'a> {
    scanner: &'a Scanner,
    generated: &'a [PathBuf],
    previous: &'a HashMap<PathBuf, Cached>,
    next: HashMap<PathBuf, Cached>,
    inputs: Inputs,
    /// The `.gitignore` rules of the directories being walked, outermost
    /// first. The CLI selects the same files with `internal/watchignore`.
    rules: Vec<gitignore::Rule>,
    racy_after: SystemTime,
}

impl Walk<'_> {
    fn entry(
        &mut self,
        relative: &Path,
        names: &mut Vec<String>,
        ignoring: Option<usize>,
    ) -> Result<()> {
        let top = relative.as_os_str().is_empty();
        if !top && ignored(relative, self.generated) {
            return Ok(());
        }
        let path = self.scanner.root.join(relative);
        let info = match fs::symlink_metadata(&path) {
            Ok(info) => info,
            Err(error) if error.kind() == io::ErrorKind::NotFound && !top => return Ok(()),
            Err(error) => return Err(error.into()),
        };
        if info.is_dir() && !top && fs::symlink_metadata(path.join(".git")).is_ok() {
            return Ok(());
        }
        let parts: Vec<&str> = names.iter().map(String::as_str).collect();
        if !top && gitignore::ignored(&self.rules, &parts, info.is_dir(), ignoring) {
            return Ok(());
        }
        if info.is_dir() {
            let ignoring = gitignore::ancestor(&self.rules, &parts, ignoring);
            let outer = self.rules.len();
            if let Ok(text) = fs::read(path.join(".gitignore")) {
                self.rules.extend(gitignore::parse(
                    names.len(),
                    &String::from_utf8_lossy(&text),
                ));
            }
            for entry in fs::read_dir(path)? {
                let name = entry?.file_name();
                names.push(name.to_string_lossy().into_owned());
                let result = self.entry(&relative.join(&name), names, ignoring);
                names.pop();
                result?;
            }
            self.rules.truncate(outer);
        } else if info.is_file() {
            let stamp = Stamp {
                device: info.dev(),
                inode: info.ino(),
                size: info.size(),
                mode: info.mode(),
                modified: (info.mtime(), info.mtime_nsec()),
                changed: (info.ctime(), info.ctime_nsec()),
            };
            let digest = match self.previous.get(relative) {
                Some(cached) if cached.stamp == stamp && !cached.racy => cached.digest,
                _ => match sha256_file(&path) {
                    Ok(digest) => {
                        self.scanner.hashed.fetch_add(1, Ordering::Relaxed);
                        digest
                    }
                    Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
                    Err(error) => return Err(error.into()),
                },
            };
            let racy = info
                .modified()
                .ok()
                .into_iter()
                .chain(changed_time(&info))
                .any(|time| time >= self.racy_after);
            self.next.insert(
                relative.into(),
                Cached {
                    stamp,
                    digest,
                    racy,
                },
            );
            self.inputs.insert(
                relative.into(),
                Input {
                    digest,
                    mode: info.permissions().mode() & 0o777,
                },
            );
        } else {
            ensure!(
                !declaration(relative) && !matches!(extension(relative), "go" | "rs"),
                "worker source must be a regular file: {}",
                relative.display()
            );
        }
        Ok(())
    }
}

fn changed_time(info: &fs::Metadata) -> Option<SystemTime> {
    let seconds = u64::try_from(info.ctime()).ok()?;
    let nanos = u32::try_from(info.ctime_nsec()).ok()?;
    SystemTime::UNIX_EPOCH.checked_add(Duration::new(seconds, nanos))
}

pub(crate) fn extension(path: &Path) -> &str {
    path.extension()
        .and_then(|value| value.to_str())
        .unwrap_or("")
}

pub(crate) fn declaration(path: &Path) -> bool {
    matches!(
        path.file_name()
            .and_then(|name| name.to_str())
            .unwrap_or(""),
        ".scenery.json"
            | "go.mod"
            | "go.sum"
            | "go.work"
            | "go.work.sum"
            | "Cargo.toml"
            | "Cargo.lock"
            | "rust-toolchain"
            | "rust-toolchain.toml"
            | "package.json"
            | "bun.lock"
            | "bun.lockb"
            | "package-lock.json"
            | "tsconfig.json"
            | "bunfig.toml"
    ) || extension(path) == "scn"
        || path.parent().is_some_and(|dir| dir.ends_with(".cargo"))
}

pub(crate) fn changed(before: &Inputs, after: &Inputs) -> Vec<PathBuf> {
    before
        .iter()
        .filter(|(path, _)| !after.contains_key(*path))
        .map(|(path, _)| path.clone())
        .chain(
            after
                .iter()
                .filter(|(path, input)| before.get(*path) != Some(*input))
                .map(|(path, _)| path.clone()),
        )
        .collect()
}

pub(crate) fn digest(inputs: &Inputs) -> String {
    let mut hash = Sha256::new();
    // Go orders relative paths by their raw bytes. PathBuf orders components,
    // which puts `service/file.go` before `service-extra/file.go`.
    let mut ordered: Vec<_> = inputs.iter().collect();
    ordered.sort_unstable_by_key(|(path, _)| path.as_os_str().as_bytes());
    for (path, input) in ordered {
        hash.update(path.as_os_str().as_bytes());
        hash.update(format!("\0{}:{:o}\n", hex(&input.digest), input.mode));
    }
    hex(&hash.finalize())
}

pub(crate) fn sync(
    root: &Path,
    workspace: &Path,
    path: &Path,
    input: Option<&Input>,
) -> Result<()> {
    let source = root.join(path);
    let target = workspace.join(path);
    let parent = target.parent().expect("source path has a parent");
    fs::create_dir_all(parent)?;
    ensure!(
        parent.canonicalize()? == parent,
        "build input destination must stay in the workspace"
    );
    if let Some(input) = input {
        let bytes = fs::read(&source)?;
        ensure!(
            source.canonicalize()? == source
                && <[u8; 32]>::from(Sha256::digest(&bytes)) == input.digest,
            "application changed while workers were compiling"
        );
        if fs::read(&target).is_ok_and(|previous| previous == bytes)
            && fs::symlink_metadata(&target)
                .is_ok_and(|info| info.is_file() && info.permissions().mode() & 0o777 == input.mode)
        {
            return Ok(());
        }
        use std::io::Write as _;
        let mut staging = tempfile::NamedTempFile::new_in(parent)?;
        staging
            .as_file()
            .set_permissions(fs::Permissions::from_mode(input.mode))?;
        staging.write_all(&bytes)?;
        staging.persist(target)?;
    } else if let Err(error) = fs::remove_file(target)
        && error.kind() != io::ErrorKind::NotFound
    {
        return Err(error.into());
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn digest_matches_go_byte_order() {
        let inputs = [("service/file.go", 0x22), ("service-extra/file.go", 0x11)]
            .into_iter()
            .map(|(path, byte)| {
                (
                    PathBuf::from(path),
                    Input {
                        digest: [byte; 32],
                        mode: 0o644,
                    },
                )
            })
            .collect();
        assert_eq!(
            digest(&inputs),
            "ff92a1e62b20b27d4871c12d4af42f681ccd5f5329c85f49a2996f72328a25cd"
        );
    }

    #[test]
    fn byte_identical_saves_leave_snapshot_unchanged() -> Result<()> {
        let root = tempfile::tempdir()?;
        let path = root.path().join("source.go");
        fs::write(&path, "package source")?;
        let scanner = Scanner::new(root.path().into());
        let initial = scanner.scan(&[])?;
        fs::write(&path, "package source")?;
        assert!(changed(&initial, &scanner.scan(&[])?).is_empty());
        fs::write(&path, "package edited")?;
        assert_eq!(
            changed(&initial, &scanner.scan(&[])?),
            [Path::new("source.go")]
        );
        Ok(())
    }

    /// Moves a file's timestamps out of the racy window, as time would.
    fn age(path: &Path) -> Result<()> {
        let past = SystemTime::now() - Duration::from_secs(60);
        fs::File::options()
            .write(true)
            .open(path)?
            .set_modified(past)?;
        Ok(())
    }

    #[test]
    fn unchanged_files_are_not_read_again() -> Result<()> {
        let root = tempfile::tempdir()?;
        for name in ["a.go", "b.go", "assets/large.bin"] {
            let path = root.path().join(name);
            fs::create_dir_all(path.parent().unwrap())?;
            fs::write(&path, name)?;
            age(&path)?;
        }
        let scanner = Scanner {
            racy_window: Duration::from_millis(40),
            ..Scanner::new(root.path().into())
        };
        let first = scanner.scan(&[])?;
        assert_eq!((first.len(), scanner.hashed()), (3, 3));
        // The status change of `age` is recent, so one more scan reads the
        // files before their stamps are trusted.
        std::thread::sleep(Duration::from_millis(80));
        assert!(scanner.scan(&[])? == first);
        let settled = scanner.hashed();
        for _ in 0..3 {
            assert!(scanner.scan(&[])? == first);
        }
        assert_eq!(scanner.hashed(), settled, "an idle tree costs no hashing");

        // One edit reads one file, and a removed file leaves the snapshot.
        fs::write(root.path().join("b.go"), "edited")?;
        fs::remove_file(root.path().join("a.go"))?;
        let next = scanner.scan(&[])?;
        assert_eq!(scanner.hashed(), settled + 1);
        let mut paths = changed(&first, &next);
        paths.sort();
        assert_eq!(paths, [Path::new("a.go"), Path::new("b.go")]);
        Ok(())
    }

    #[test]
    fn a_rewrite_with_the_same_size_and_time_is_still_seen() -> Result<()> {
        let root = tempfile::tempdir()?;
        let path = root.path().join("source.go");
        fs::write(&path, "first")?;
        let scanner = Scanner::new(root.path().into());
        let before = scanner.scan(&[])?;
        // Same length, written within the timestamp resolution of the scan:
        // restore the modification time a coarse clock would have kept.
        let modified = fs::metadata(&path)?.modified()?;
        fs::write(&path, "other")?;
        fs::File::options()
            .write(true)
            .open(&path)?
            .set_modified(modified)?;
        assert_eq!(
            changed(&before, &scanner.scan(&[])?),
            [Path::new("source.go")]
        );
        Ok(())
    }

    /// The CLI's Go scan and this one must select the same files, or their
    /// source digests differ and the host refuses the CLI's plan. Both run
    /// the cases of this fixture.
    #[test]
    fn scope_matches_the_cli_fixture() -> Result<()> {
        #[derive(serde::Deserialize)]
        struct Case {
            name: String,
            #[serde(default)]
            generated: Vec<PathBuf>,
            files: BTreeMap<String, String>,
            inputs: Vec<String>,
        }
        let cases: Vec<Case> = serde_json::from_str(include_str!(
            "../../../../cmd/scenery/testdata/worker-input-scope.json"
        ))?;
        assert!(!cases.is_empty());
        for case in cases {
            let root = tempfile::tempdir()?;
            for (name, content) in &case.files {
                let path = root.path().join(name);
                fs::create_dir_all(path.parent().unwrap())?;
                fs::write(path, content)?;
            }
            let scanned = Scanner::new(root.path().into()).scan(&case.generated)?;
            let mut inputs: Vec<_> = scanned
                .keys()
                .map(|path| path.to_string_lossy().into_owned())
                .collect();
            inputs.sort();
            assert_eq!(inputs, case.inputs, "{}", case.name);
        }
        Ok(())
    }
}
