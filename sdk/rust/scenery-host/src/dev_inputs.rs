use std::collections::BTreeMap;
use std::fs;
use std::io;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};

use anyhow::{Result, ensure};
use sha2::{Digest, Sha256};

use crate::process::hex;

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

pub(crate) fn scan(root: &Path, generated: &[PathBuf]) -> Result<Inputs> {
    let mut inputs = Inputs::new();
    scan_path(root, Path::new(""), generated, &mut inputs)?;
    Ok(inputs)
}

fn scan_path(
    root: &Path,
    relative: &Path,
    generated: &[PathBuf],
    inputs: &mut Inputs,
) -> Result<()> {
    if !relative.as_os_str().is_empty() && ignored(relative, generated) {
        return Ok(());
    }
    let path = root.join(relative);
    let result = match fs::symlink_metadata(&path) {
        Ok(info) => info,
        Err(error)
            if error.kind() == io::ErrorKind::NotFound && !relative.as_os_str().is_empty() =>
        {
            return Ok(());
        }
        Err(error) => return Err(error.into()),
    };
    if result.is_dir() {
        if !relative.as_os_str().is_empty() && fs::symlink_metadata(path.join(".git")).is_ok() {
            return Ok(());
        }
        for entry in fs::read_dir(path)? {
            scan_path(root, &relative.join(entry?.file_name()), generated, inputs)?;
        }
    } else if result.is_file() {
        match fs::read(path) {
            Ok(bytes) => {
                inputs.insert(
                    relative.into(),
                    Input {
                        digest: Sha256::digest(bytes).into(),
                        mode: result.permissions().mode() & 0o777,
                    },
                );
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => {}
            Err(error) => return Err(error.into()),
        }
    } else {
        ensure!(
            !declaration(relative) && !matches!(extension(relative), "go" | "rs"),
            "worker source must be a regular file: {}",
            relative.display()
        );
    }
    Ok(())
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
    for (path, input) in inputs {
        hash.update(format!(
            "{}\0{}:{:o}\n",
            path.display(),
            hex(&input.digest),
            input.mode
        ));
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
    fn byte_identical_saves_leave_snapshot_unchanged() -> Result<()> {
        let root = tempfile::tempdir()?;
        let path = root.path().join("source.go");
        fs::write(&path, "package source")?;
        let initial = scan(root.path(), &[])?;
        fs::write(&path, "package source")?;
        assert!(changed(&initial, &scan(root.path(), &[])?).is_empty());
        fs::write(&path, "package edited")?;
        assert_eq!(
            changed(&initial, &scan(root.path(), &[])?),
            [Path::new("source.go")]
        );
        Ok(())
    }
}
