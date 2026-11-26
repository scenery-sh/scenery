//! Production startup configuration: command-line arguments, the runtime
//! configuration snapshot, and the `{"$env": "NAME"}` references that let a
//! container or service manager inject secrets through its environment.

use std::ffi::{OsStr, OsString};
use std::fs::File;
use std::io::Read;
use std::os::fd::{FromRawFd, OwnedFd, RawFd};
use std::os::unix::ffi::OsStrExt;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result, bail, ensure};
use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use serde_json::Value;

use crate::process::CONFIG_SNAPSHOT_FD;
use crate::values;

/// Names a runtime configuration snapshot file when `--config` is absent.
pub(crate) const CONFIG_FILE: &str = "SCENERY_CONFIG_FILE";
/// The listener address when neither `--listen` nor `--port` is given.
pub(crate) const LISTEN_ADDR: &str = "SCENERY_LISTEN_ADDR";
/// `tcp` (the default) or `unix`.
pub(crate) const LISTEN_NETWORK: &str = "SCENERY_LISTEN_NETWORK";

const SNAPSHOT_LIMIT: u64 = 4 << 20;
const SNAPSHOT_KIND: &str = "scenery.config.snapshot";
const DEFAULT_LISTEN: &str = "127.0.0.1:4000";

/// Reads one environment variable. Production passes `std::env::var_os`;
/// tests pass a fixed map, so they never mutate the process environment.
pub(crate) type Lookup<'a> = &'a dyn Fn(&str) -> Option<OsString>;

/// The arguments of a production host: `--listen <address>`,
/// `--port <port>` (loopback only) and `--config <path>`. Each also takes
/// the `--name=value` form and Go's single-dash spelling.
#[derive(Debug, Default, PartialEq)]
pub(crate) struct Arguments {
    pub listen: Option<String>,
    pub port: Option<u16>,
    pub config: Option<PathBuf>,
}

impl Arguments {
    pub(crate) fn parse(args: impl IntoIterator<Item = OsString>) -> Result<Self> {
        let mut parsed = Self::default();
        let mut args = args.into_iter();
        while let Some(arg) = args.next() {
            let arg = arg.into_string().ok().context("invalid host argument")?;
            let (name, assigned) = arg
                .split_once('=')
                .map_or((arg.as_str(), None), |(key, value)| (key, Some(value)));
            let name = name.strip_prefix("--").or_else(|| name.strip_prefix('-'));
            let Some(name @ ("listen" | "port" | "config")) = name else {
                bail!("unknown host argument {arg}");
            };
            let value = match assigned {
                Some(value) => value.to_owned(),
                None => args
                    .next()
                    .and_then(|value| value.into_string().ok())
                    .with_context(|| format!("missing value for --{name}"))?,
            };
            ensure!(!value.is_empty(), "empty value for --{name}");
            let duplicate = match name {
                "listen" => parsed.listen.replace(value).is_some(),
                "port" => parsed
                    .port
                    .replace(value.parse().context("invalid --port")?)
                    .is_some(),
                _ => parsed.config.replace(PathBuf::from(value)).is_some(),
            };
            ensure!(!duplicate, "--{name} is given more than once");
        }
        ensure!(
            parsed.listen.is_none() || parsed.port.is_none(),
            "--listen and --port are mutually exclusive"
        );
        Ok(parsed)
    }

    /// The public listener address: `--listen`, then `--port` on loopback,
    /// then `SCENERY_LISTEN_ADDR`, then `127.0.0.1:4000`. A TCP address
    /// must name a port; a container binds all interfaces with
    /// `--listen 0.0.0.0:4000`.
    pub(crate) fn listen_address(&self, lookup: Lookup) -> Result<String> {
        let address = self
            .listen
            .clone()
            .or_else(|| self.port.map(|port| format!("127.0.0.1:{port}")))
            .or_else(|| {
                lookup(LISTEN_ADDR)
                    .and_then(|value| value.into_string().ok())
                    .filter(|value| !value.trim().is_empty())
            })
            .unwrap_or_else(|| DEFAULT_LISTEN.into());
        let network = lookup(LISTEN_NETWORK)
            .and_then(|value| value.into_string().ok())
            .unwrap_or_default();
        if network.trim().eq_ignore_ascii_case("unix") {
            ensure!(
                Path::new(&address).is_absolute(),
                "Unix listener requires an absolute socket path"
            );
            return Ok(address);
        }
        let valid = address.rsplit_once(':').is_some_and(|(host, port)| {
            !host.is_empty() && !host.contains(char::is_whitespace) && port.parse::<u16>().is_ok()
        });
        ensure!(
            valid,
            "listener address {address:?} must have the form host:port"
        );
        Ok(address)
    }
}

/// Loads the runtime configuration snapshot of a production host from, in
/// order, `--config`, `SCENERY_CONFIG_FILE`, or the inherited descriptor
/// named by `SCENERY_CONFIG_SNAPSHOT_FD`. Without any of them the host runs
/// with an empty configuration. An explicitly named source that cannot be
/// read, parsed, or resolved stops the host before it starts any worker.
pub(crate) fn load_snapshot(explicit: Option<&Path>, lookup: Lookup) -> Result<Value> {
    let file = explicit.map(Path::to_owned).or_else(|| {
        lookup(CONFIG_FILE)
            .filter(|value| !value.is_empty())
            .map(PathBuf::from)
    });
    let descriptor = lookup(CONFIG_SNAPSHOT_FD).filter(|value| !value.is_empty());
    let (bytes, source) = match (file, descriptor) {
        (Some(_), Some(_)) => bail!(
            "the configuration snapshot is given both as a file and as descriptor {CONFIG_SNAPSHOT_FD}"
        ),
        (Some(path), None) => (
            read_file(&path)?,
            format!("configuration file {}", path.display()),
        ),
        (None, Some(fd)) => (read_descriptor(&fd)?, "configuration descriptor".into()),
        (None, None) => return Ok(Value::Null),
    };
    let mut snapshot = values::parse(&bytes).with_context(|| format!("parse {source}"))?;
    resolve_snapshot(&mut snapshot, lookup).with_context(|| format!("resolve {source}"))?;
    Ok(snapshot)
}

fn read_file(path: &Path) -> Result<Vec<u8>> {
    let file =
        File::open(path).with_context(|| format!("read configuration file {}", path.display()))?;
    ensure!(
        file.metadata()?.is_file(),
        "configuration file {} is not a regular file",
        path.display()
    );
    read_limited(file).with_context(|| format!("read configuration file {}", path.display()))
}

fn read_descriptor(value: &OsStr) -> Result<Vec<u8>> {
    let fd: RawFd = value
        .to_str()
        .context("invalid configuration descriptor")?
        .trim()
        .parse()
        .context("invalid configuration descriptor")?;
    ensure!(
        fd >= 3 && unsafe { libc::fcntl(fd, libc::F_GETFD) } != -1,
        "configuration descriptor is unavailable"
    );
    let fd = unsafe { OwnedFd::from_raw_fd(fd) };
    read_limited(File::from(fd))
}

fn read_limited(file: File) -> Result<Vec<u8>> {
    let mut bytes = Vec::new();
    file.take(SNAPSHOT_LIMIT + 1).read_to_end(&mut bytes)?;
    ensure!(
        bytes.len() as u64 <= SNAPSHOT_LIMIT,
        "configuration snapshot exceeds 4 MiB"
    );
    Ok(bytes)
}

/// Checks the snapshot identity and resolves its environment references.
/// A secret resolves to the variable's bytes, which the snapshot carries in
/// base64 as every other secret; any other reference resolves to a string.
fn resolve_snapshot(snapshot: &mut Value, lookup: Lookup) -> Result<()> {
    ensure!(
        snapshot["kind"] == SNAPSHOT_KIND,
        "configuration snapshot kind must be {SNAPSHOT_KIND:?}"
    );
    for field in ["app_id", "environment", "revision"] {
        ensure!(
            snapshot[field]
                .as_str()
                .is_some_and(|value| !value.is_empty()),
            "configuration snapshot has no {field}"
        );
    }
    if let Some(secrets) = snapshot.get_mut("secrets") {
        let secrets = secrets
            .as_object_mut()
            .context("configuration snapshot secrets must be an object")?;
        for (key, secret) in secrets {
            if let Some(name) = reference(secret)? {
                let raw = variable(lookup, name)?;
                *secret = Value::String(STANDARD.encode(raw.as_bytes()));
            } else {
                ensure!(
                    secret
                        .as_str()
                        .is_some_and(|text| STANDARD.decode(text).is_ok()),
                    "configuration secret {key} must be base64 or an environment reference"
                );
            }
        }
    }
    resolve_env(snapshot, lookup)
}

/// Replaces every `{"$env": "NAME"}` object in `value` with the named
/// variable's value as a JSON string. A referenced variable that is not set
/// is an error naming the variable, never its value.
pub(crate) fn resolve_env(value: &mut Value, lookup: Lookup) -> Result<()> {
    if let Some(name) = reference(value)? {
        let raw = variable(lookup, name)?;
        let text = raw
            .into_string()
            .map_err(|_| anyhow::anyhow!("environment variable {name} is not valid UTF-8"))?;
        *value = Value::String(text);
        return Ok(());
    }
    match value {
        Value::Array(items) => items
            .iter_mut()
            .try_for_each(|item| resolve_env(item, lookup)),
        Value::Object(fields) => fields
            .values_mut()
            .try_for_each(|field| resolve_env(field, lookup)),
        _ => Ok(()),
    }
}

/// The variable a `{"$env": "NAME"}` object references. Any other object
/// that uses the `$env` member is malformed rather than literal data.
fn reference(value: &Value) -> Result<Option<&str>> {
    let Some(fields) = value.as_object() else {
        return Ok(None);
    };
    let Some(name) = fields.get("$env") else {
        return Ok(None);
    };
    let name = name
        .as_str()
        .filter(|name| fields.len() == 1 && valid_name(name))
        .context(r#"an environment reference must be exactly {"$env": "NAME"}"#)?;
    Ok(Some(name))
}

fn valid_name(name: &str) -> bool {
    let mut bytes = name.bytes();
    bytes
        .next()
        .is_some_and(|first| first == b'_' || first.is_ascii_alphabetic())
        && bytes.all(|byte| byte == b'_' || byte.is_ascii_alphanumeric())
}

fn variable(lookup: Lookup, name: &str) -> Result<OsString> {
    lookup(name).with_context(|| {
        format!("configuration references environment variable {name}, which is not set")
    })
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;
    use std::io::Write;
    use std::os::fd::IntoRawFd;

    use serde_json::json;

    use super::*;

    fn environment(pairs: &[(&str, &str)]) -> impl Fn(&str) -> Option<OsString> + use<> {
        let map: BTreeMap<String, OsString> = pairs
            .iter()
            .map(|(key, value)| ((*key).to_owned(), OsString::from(value)))
            .collect();
        move |name| map.get(name).cloned()
    }

    fn snapshot_file(contents: &[u8]) -> tempfile::NamedTempFile {
        let mut file = tempfile::NamedTempFile::new().unwrap();
        file.write_all(contents).unwrap();
        file
    }

    const SNAPSHOT: &str = r#"{"kind":"scenery.config.snapshot","app_id":"shop","environment":"production",
        "revision":"r1","values":{"region":{"$env":"REGION"},"limits":{"burst":[{"$env":"BURST"}]}},
        "secrets":{"stripe":{"$env":"STRIPE_KEY"},"plain":"c2VjcmV0"},"public":["region"]}"#;

    #[test]
    fn arguments_accept_container_listeners_and_reject_ambiguity() {
        let parse = |args: &[&str]| Arguments::parse(args.iter().map(OsString::from));
        let parsed = parse(&["--listen", "0.0.0.0:4000", "--config=/etc/app/config.json"]).unwrap();
        assert_eq!(parsed.listen.as_deref(), Some("0.0.0.0:4000"));
        assert_eq!(parsed.config, Some(PathBuf::from("/etc/app/config.json")));
        let none = environment(&[]);
        assert_eq!(parsed.listen_address(&none).unwrap(), "0.0.0.0:4000");
        assert_eq!(
            parse(&["-port", "8080"])
                .unwrap()
                .listen_address(&none)
                .unwrap(),
            "127.0.0.1:8080"
        );
        assert_eq!(
            parse(&["--listen=[::]:4000"])
                .unwrap()
                .listen_address(&none)
                .unwrap(),
            "[::]:4000"
        );
        let from_env = environment(&[(LISTEN_ADDR, "0.0.0.0:9000")]);
        assert_eq!(
            parse(&[]).unwrap().listen_address(&from_env).unwrap(),
            "0.0.0.0:9000"
        );
        assert_eq!(
            parse(&[]).unwrap().listen_address(&none).unwrap(),
            DEFAULT_LISTEN
        );
        let unix = environment(&[(LISTEN_NETWORK, "unix"), (LISTEN_ADDR, "/run/app.sock")]);
        assert_eq!(
            parse(&[]).unwrap().listen_address(&unix).unwrap(),
            "/run/app.sock"
        );
        for invalid in [
            &["--listen", "0.0.0.0:4000", "--port", "4000"][..],
            &["--config", "a", "--config", "b"],
            &["--config"],
            &["--config="],
            &["--verbose"],
            &["--port", "70000"],
        ] {
            assert!(parse(invalid).is_err(), "accepted {invalid:?}");
        }
        assert!(
            parse(&["--listen", "4000"])
                .unwrap()
                .listen_address(&none)
                .is_err()
        );
    }

    #[test]
    fn config_file_resolves_environment_references() {
        let file = snapshot_file(SNAPSHOT.as_bytes());
        let lookup = environment(&[
            ("REGION", "eu-central-1"),
            ("BURST", "40"),
            ("STRIPE_KEY", "sk_live_123"),
        ]);
        let snapshot = load_snapshot(Some(file.path()), &lookup).unwrap();
        assert_eq!(snapshot["values"]["region"], json!("eu-central-1"));
        assert_eq!(snapshot["values"]["limits"]["burst"][0], json!("40"));
        assert_eq!(
            snapshot["secrets"]["stripe"],
            json!(STANDARD.encode("sk_live_123"))
        );
        assert_eq!(snapshot["secrets"]["plain"], json!("c2VjcmV0"));

        // The environment variable names the same file when --config is absent.
        let path = file.path().to_str().unwrap();
        let named = environment(&[
            (CONFIG_FILE, path),
            ("REGION", "us-east-1"),
            ("BURST", "1"),
            ("STRIPE_KEY", "k"),
        ]);
        assert_eq!(
            load_snapshot(None, &named).unwrap()["values"]["region"],
            json!("us-east-1")
        );
        assert_eq!(load_snapshot(None, &environment(&[])).unwrap(), Value::Null);
    }

    #[test]
    fn missing_variables_and_malformed_snapshots_fail_without_leaking_values() {
        let file = snapshot_file(SNAPSHOT.as_bytes());
        let lookup = environment(&[("REGION", "eu"), ("BURST", "1")]);
        let error = format!(
            "{:#}",
            load_snapshot(Some(file.path()), &lookup).unwrap_err()
        );
        assert!(error.contains("STRIPE_KEY, which is not set"), "{error}");

        let secret = environment(&[("REGION", "eu"), ("BURST", "1"), ("STRIPE_KEY", "hunter2")]);
        for (contents, expected) in [
            (r#"{"kind":"other"}"#.to_owned(), "kind must be"),
            (
                r#"{"kind":"scenery.config.snapshot","app_id":"a","environment":"e"}"#.into(),
                "no revision",
            ),
            (
                SNAPSHOT.replace(r#"{"$env":"REGION"}"#, r#"{"$env":"REGION","x":1}"#),
                "exactly",
            ),
            (
                SNAPSHOT.replace(r#"{"$env":"REGION"}"#, r#"{"$env":"1BAD"}"#),
                "exactly",
            ),
            (
                SNAPSHOT.replace(r#""c2VjcmV0""#, r#""not base64!""#),
                "must be base64",
            ),
            ("{".into(), "parse configuration file"),
        ] {
            let file = snapshot_file(contents.as_bytes());
            let error = format!(
                "{:#}",
                load_snapshot(Some(file.path()), &secret).unwrap_err()
            );
            assert!(error.contains(expected), "{expected}: {error}");
            assert!(!error.contains("hunter2"), "{error}");
        }
        let missing = Path::new("/nonexistent/scenery-config.json");
        let error = format!("{:#}", load_snapshot(Some(missing), &secret).unwrap_err());
        assert!(
            error.contains("read configuration file /nonexistent"),
            "{error}"
        );
    }

    #[test]
    fn snapshots_are_limited_to_four_mebibytes() {
        let lookup = environment(&[("REGION", "eu"), ("BURST", "1"), ("STRIPE_KEY", "k")]);
        let mut padded = SNAPSHOT.as_bytes().to_vec();
        padded.resize(SNAPSHOT_LIMIT as usize, b' ');
        let file = snapshot_file(&padded);
        assert!(load_snapshot(Some(file.path()), &lookup).is_ok());
        padded.push(b' ');
        let file = snapshot_file(&padded);
        let error = format!(
            "{:#}",
            load_snapshot(Some(file.path()), &lookup).unwrap_err()
        );
        assert!(error.contains("exceeds 4 MiB"), "{error}");
    }

    #[test]
    fn inherited_descriptor_is_the_fallback_and_excludes_a_file() {
        let fd = snapshot_file(SNAPSHOT.as_bytes())
            .reopen()
            .unwrap()
            .into_raw_fd()
            .to_string();
        let lookup = environment(&[
            (CONFIG_SNAPSHOT_FD, &fd),
            ("REGION", "eu"),
            ("BURST", "1"),
            ("STRIPE_KEY", "k"),
        ]);
        let snapshot = load_snapshot(None, &lookup).unwrap();
        assert_eq!(snapshot["values"]["region"], json!("eu"));

        let file = snapshot_file(SNAPSHOT.as_bytes());
        let both = environment(&[(CONFIG_SNAPSHOT_FD, "3"), ("REGION", "eu")]);
        let error = load_snapshot(Some(file.path()), &both).unwrap_err();
        assert!(error.to_string().contains("both"), "{error}");
        let closed = environment(&[(CONFIG_SNAPSHOT_FD, "999")]);
        assert!(load_snapshot(None, &closed).is_err());
    }
}
