use std::collections::{BTreeMap, BTreeSet};
use std::fs::File;
use std::io::{self, Read, Write};
use std::os::unix::fs::MetadataExt;
use std::path::{Component, Path};
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use http_body_util::{BodyExt, StreamBody};
use hyper::body::{Frame, Incoming};
use hyper::{HeaderMap, Method, Request, StatusCode};
use rustix::fs::{self, AtFlags, Dir, FlockOperation, Mode, OFlags};
use rustix::io::Errno;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use time::OffsetDateTime;
use tokio::io::{AsyncReadExt, AsyncSeekExt, AsyncWriteExt};
use tokio_util::io::ReaderStream;

use crate::host::{Fault, Reply, empty, header, json_reply, stamp};
use crate::process::{hex, random_hex};
use crate::values;

const OBJECT_FORMAT: &str = r#"{"store":"string","tenant":"string","key":"string","size_bytes":"integer","content_type":"string","etag":"string","sha256":"string","modified_at":"date-time","metadata":{"additionalProperties":"string"}}"#;
const RECORD_LIMIT: usize = 64 << 10;
const PRIVATE_FILE: Mode = Mode::RUSR.union(Mode::WUSR);
const PRIVATE_DIR: Mode = PRIVATE_FILE.union(Mode::XUSR);

pub(crate) struct Selection<'a> {
    pub config: &'a Value,
    pub store: &'a Value,
    pub name: &'a str,
    pub tenant: &'a str,
    pub key: Option<&'a str>,
    pub query: &'a BTreeMap<String, Vec<String>>,
}

pub(crate) async fn serve(
    request: Request<Incoming>,
    selection: Selection<'_>,
) -> Result<Reply, Fault> {
    let Selection {
        config,
        store,
        name,
        tenant,
        key,
        query,
    } = selection;
    if tenant.len() > 256
        || tenant.chars().any(char::is_control)
        || key.is_some_and(|key| !path_valid(key, false))
    {
        return Err(Fault::invalid());
    }
    let owner = Namespace::open(store, &config["namespace"])
        .await
        .map_err(Fault::internal)?;
    let partition = Partition::new(&owner, name, tenant);
    let is_mutation = matches!(*request.method(), Method::PUT | Method::DELETE);
    if is_mutation && store["access"] == "read_only" {
        return Err(Fault::forbidden());
    }
    let Some(key) = key else {
        return list(&partition, query).await;
    };
    let reference = partition.reference_path(key);
    match *request.method() {
        Method::GET | Method::HEAD => {
            let current = partition
                .current(&reference, key)
                .map_err(Fault::internal)?
                .ok_or_else(|| Fault::new(404, "not_found", "storage object is unavailable"))?;
            let mut reply = object_reply(&current["object"])?;
            if request.method() == Method::GET {
                let version = current["version_id"]
                    .as_str()
                    .ok_or_else(|| Fault::internal("missing storage version"))?;
                let payload = partition.version_path(key, version);
                let file = open_file(&owner.generation, &payload, OFlags::RDONLY)
                    .map_err(Fault::internal)?;
                let size = current["object"]["size_bytes"]
                    .as_u64()
                    .ok_or_else(|| Fault::internal("invalid storage object size"))?;
                if file.metadata().map_err(Fault::internal)?.len() != size {
                    return Err(Fault::internal(
                        "storage payload length does not match its reference",
                    ));
                }
                let offset = get_usize(query, "offset")?.unwrap_or(0);
                let length = get_usize(query, "length")?.unwrap_or(u64::MAX);
                if offset > size {
                    return Err(Fault::invalid());
                }
                let length = (size - offset).min(length);
                let mut file = tokio::fs::File::from_std(file);
                file.seek(io::SeekFrom::Start(offset))
                    .await
                    .map_err(Fault::internal)?;
                header(reply.headers_mut(), "content-length", &length.to_string());
                let stream = futures_util::StreamExt::map(
                    ReaderStream::new(file.take(length)),
                    move |chunk| {
                        let _keep_lease = &owner;
                        chunk.map(Frame::data)
                    },
                );
                *reply.body_mut() = StreamBody::new(stream).boxed_unsync();
            }
            Ok(reply)
        }
        Method::PUT => {
            put(
                request,
                &partition,
                key,
                &reference,
                store["max_object_bytes"].as_u64(),
            )
            .await
        }
        Method::DELETE => {
            let _lock = owner.lock(true).await.map_err(Fault::internal)?;
            let current = partition
                .current(&reference, key)
                .map_err(Fault::internal)?;
            precondition(request.headers(), current.as_ref())?;
            if let Some(parent) = current.and_then(|_| reference.parent()) {
                let dir = directory(&owner.generation, parent, false).map_err(Fault::internal)?;
                fs::unlinkat(
                    &dir,
                    reference.file_name().expect("reference has a name"),
                    AtFlags::empty(),
                )
                .map_err(Fault::internal)?;
                dir.sync_all().map_err(Fault::internal)?;
            }
            Ok(empty(StatusCode::NO_CONTENT))
        }
        _ => Err(Fault::invalid()),
    }
}

struct Namespace {
    root: File,
    _maintenance: File,
    generation: File,
    generation_id: String,
    incarnation: String,
    identity: Value,
}

impl Namespace {
    async fn open(store: &Value, config: &Value) -> Result<Self> {
        let root = Path::new(store["root"].as_str().context("missing storage root")?);
        ensure!(
            root.is_absolute() && root.canonicalize()? == root,
            "unsafe storage root"
        );
        if let Some(expected) = config["root"].as_str() {
            ensure!(
                root == Path::new(expected),
                "storage root does not match namespace"
            );
        }
        let root = File::from(fs::open(
            root,
            OFlags::DIRECTORY | OFlags::NOFOLLOW | OFlags::CLOEXEC,
            Mode::empty(),
        )?);
        owned(&root, true)?;
        let maintenance = lock(&root, "maintenance.lock", false).await?;
        match fs::statat(&root, "operation.json", AtFlags::SYMLINK_NOFOLLOW) {
            Err(Errno::NOENT) => {}
            _ => anyhow::bail!("storage recovery is required"),
        }
        let identity = record(&root, Path::new("owner.json"))?;
        let generation_id = identity["generation"]
            .as_str()
            .context("missing storage generation")?
            .to_owned();
        let incarnation = identity["incarnation"]
            .as_str()
            .context("missing storage incarnation")?
            .to_owned();
        ensure!(
            valid_id(&generation_id)
                && valid_id(&incarnation)
                && identity["kind"] == "scenery.storage.owner"
                && identity["state"] == "ready"
                && identity["binding"]["user_id"] == rustix::process::getuid().as_raw(),
            "retained storage owner is invalid"
        );
        ensure!(
            config.is_object()
                && config["binding"] == identity["binding"]
                && config["incarnation"] == incarnation,
            "storage ownership changed"
        );
        let generation = directory(&root, &Path::new("generations").join(&generation_id), false)?;
        let marker = record(&generation, Path::new("generation.json"))?;
        ensure!(
            marker["kind"] == "scenery.storage.generation"
                && marker["binding"] == identity["binding"]
                && marker["incarnation"] == incarnation
                && marker["generation"] == generation_id,
            "retained storage generation is invalid"
        );
        Ok(Self {
            root,
            _maintenance: maintenance,
            generation,
            generation_id,
            incarnation,
            identity,
        })
    }

    async fn lock(&self, exclusive: bool) -> Result<File> {
        lock(&self.root, "mutation.lock", exclusive).await
    }
}

struct Partition<'a> {
    owner: &'a Namespace,
    name: &'a str,
    tenant: &'a str,
    path: std::path::PathBuf,
}

impl<'a> Partition<'a> {
    fn new(owner: &'a Namespace, name: &'a str, tenant: &'a str) -> Self {
        let tenant_token = if tenant.is_empty() {
            "unscoped".into()
        } else {
            digest("tenant", &[tenant])
        };
        Self {
            owner,
            name,
            tenant,
            path: Path::new(&digest("store", &[name])).join(tenant_token),
        }
    }

    fn reference_path(&self, key: &str) -> std::path::PathBuf {
        let hash = digest("key", &[key]);
        Path::new("refs")
            .join(&self.path)
            .join(&hash[..2])
            .join(format!("{hash}.json"))
    }

    fn version_path(&self, key: &str, version: &str) -> std::path::PathBuf {
        let hash = digest("key", &[key]);
        Path::new("versions")
            .join(&self.path)
            .join(&hash[..2])
            .join(hash.clone())
            .join(format!("{version}.data"))
    }

    fn current(&self, path: &Path, key: &str) -> Result<Option<Value>> {
        let value = match record(&self.owner.generation, path) {
            Ok(value) => value,
            Err(error) if missing(&error) => return Ok(None),
            Err(error) => return Err(error),
        };
        self.check_reference(&value)?;
        ensure!(
            value["object"]["key"] == key,
            "storage reference tuple differs"
        );
        Ok(Some(value))
    }

    fn check_reference(&self, value: &Value) -> Result<()> {
        let object = &value["object"];
        ensure!(
            value["kind"] == "scenery.storage.reference"
                && value["schema_revision"] == reference_revision()
                && value["version_id"].as_str().is_some_and(valid_id)
                && object["store"] == self.name
                && object["tenant"].as_str().unwrap_or_default() == self.tenant
                && object["key"]
                    .as_str()
                    .is_some_and(|key| path_valid(key, false))
                && object["size_bytes"].as_u64().is_some(),
            "invalid storage reference"
        );
        Ok(())
    }
}

fn valid_id(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn path_valid(value: &str, prefix: bool) -> bool {
    if prefix && value.is_empty() {
        return true;
    }
    let normalized = if prefix {
        value.strip_suffix('/').unwrap_or(value)
    } else {
        value
    };
    value.len() <= 4096
        && !value.contains('\\')
        && !value.chars().any(char::is_control)
        && normalized
            .split('/')
            .all(|part| !matches!(part, "" | "." | ".."))
}

fn digest(domain: &str, values: &[&str]) -> String {
    let domain = format!("scenery.storage.{domain}");
    let mut hash = Sha256::new();
    for value in std::iter::once(domain.as_str()).chain(values.iter().copied()) {
        hash.update((value.len() as u64).to_be_bytes());
        hash.update(value.as_bytes());
    }
    hex(&hash.finalize())
}

fn owned(file: &File, directory: bool) -> Result<()> {
    let info = file.metadata()?;
    ensure!(
        info.uid() == rustix::process::getuid().as_raw()
            && info.mode() & 0o077 == 0
            && if directory {
                info.is_dir()
            } else {
                info.is_file() && info.nlink() == 1
            },
        "storage requires exclusively owned private records"
    );
    Ok(())
}

fn directory(anchor: &File, path: &Path, create: bool) -> Result<File> {
    let mut parent = anchor.try_clone()?;
    for component in path.components() {
        let Component::Normal(name) = component else {
            anyhow::bail!("invalid relative storage path")
        };
        if create {
            match fs::mkdirat(&parent, name, PRIVATE_DIR) {
                Ok(()) => parent.sync_all()?,
                Err(Errno::EXIST) => {}
                Err(error) => return Err(error.into()),
            }
        }
        let next = File::from(fs::openat(
            &parent,
            name,
            OFlags::DIRECTORY | OFlags::NOFOLLOW | OFlags::CLOEXEC,
            Mode::empty(),
        )?);
        owned(&next, true)?;
        parent = next;
    }
    Ok(parent)
}

fn open_file(anchor: &File, path: &Path, flags: OFlags) -> Result<File> {
    let parent = directory(anchor, path.parent().context("invalid record path")?, false)?;
    let file = File::from(fs::openat(
        &parent,
        path.file_name().context("invalid record name")?,
        flags | OFlags::NOFOLLOW | OFlags::NONBLOCK | OFlags::CLOEXEC,
        Mode::empty(),
    )?);
    owned(&file, false)?;
    Ok(file)
}

async fn lock(anchor: &File, name: &str, exclusive: bool) -> Result<File> {
    let file = open_file(anchor, Path::new(name), OFlags::RDWR)?;
    let mode = if exclusive {
        FlockOperation::NonBlockingLockExclusive
    } else {
        FlockOperation::NonBlockingLockShared
    };
    loop {
        match fs::flock(&file, mode) {
            Ok(()) => return Ok(file),
            Err(Errno::AGAIN | Errno::INTR) => tokio::time::sleep(Duration::from_millis(5)).await,
            Err(error) => return Err(error.into()),
        }
    }
}

fn record(anchor: &File, path: &Path) -> Result<Value> {
    let mut output = Vec::new();
    open_file(anchor, path, OFlags::RDONLY)?
        .take(RECORD_LIMIT as u64 + 1)
        .read_to_end(&mut output)?;
    ensure!(output.len() <= RECORD_LIMIT, "storage record exceeds limit");
    values::parse(&output)
}

fn missing(error: &anyhow::Error) -> bool {
    error.downcast_ref::<Errno>() == Some(&Errno::NOENT)
}

struct Stage<'a> {
    directory: &'a File,
    name: String,
}

impl Drop for Stage<'_> {
    fn drop(&mut self) {
        let _ = fs::unlinkat(self.directory, self.name.as_str(), AtFlags::empty());
    }
}

fn create(parent: &File, name: &str) -> Result<File> {
    Ok(File::from(fs::openat(
        parent,
        name,
        OFlags::WRONLY | OFlags::CREATE | OFlags::EXCL | OFlags::CLOEXEC,
        PRIVATE_FILE,
    )?))
}

fn reference_revision() -> String {
    let descriptor =
        format!(r#"{{"identity":"artifact","object":{OBJECT_FORMAT},"version_id":"string"}}"#);
    let mut hash = Sha256::new();
    hash.update(b"scenery.schema\0");
    hash.update(values::encode(&json!(descriptor)));
    format!("sha256:{}", hex(&hash.finalize()))
}

fn precondition(headers: &HeaderMap, current: Option<&Value>) -> Result<(), Fault> {
    let expected = headers
        .get("if-match")
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default();
    let absent = headers
        .get("if-none-match")
        .is_some_and(|value| value == "*");
    if absent && !expected.is_empty() {
        return Err(Fault::invalid());
    }
    if absent && current.is_some()
        || !expected.is_empty() && current.is_none_or(|value| value["object"]["etag"] != expected)
    {
        return Err(Fault::new(
            412,
            "failed_precondition",
            "storage precondition failed",
        ));
    }
    Ok(())
}

async fn put(
    request: Request<Incoming>,
    partition: &Partition<'_>,
    key: &str,
    reference: &Path,
    limit: Option<u64>,
) -> Result<Reply, Fault> {
    let owner = partition.owner;
    let version = random_hex::<16>().map_err(Fault::internal)?;
    let staging =
        directory(&owner.generation, Path::new("staging"), false).map_err(Fault::internal)?;
    let mut file = tokio::fs::File::from_std(create(&staging, &version).map_err(Fault::internal)?);
    let stage = Stage {
        directory: &staging,
        name: version.clone(),
    };
    let (parts, mut data) = request.into_parts();
    let media_type = parts
        .headers
        .get("content-type")
        .and_then(|header| header.to_str().ok())
        .map(str::to_owned)
        .unwrap_or_else(|| {
            mime_guess::from_path(key)
                .first_or_octet_stream()
                .to_string()
        });
    if media_type.len() > 255 {
        return Err(Fault::invalid());
    }
    let metadata = parts
        .headers
        .get("x-storage-metadata")
        .map(|header| {
            let decoded = URL_SAFE_NO_PAD
                .decode(header.as_bytes())
                .map_err(|_| Fault::invalid())?;
            if decoded.len() > 16 << 10 {
                return Err(Fault::invalid());
            }
            let data = values::parse(&decoded).map_err(|_| Fault::invalid())?;
            if !data
                .as_object()
                .is_some_and(|map| map.values().all(Value::is_string))
            {
                return Err(Fault::invalid());
            }
            Ok(data)
        })
        .transpose()?;
    let mut hash = Sha256::new();
    let mut size = 0_u64;
    while let Some(frame) = data.frame().await {
        if let Ok(bytes) = frame.map_err(|_| Fault::invalid())?.into_data() {
            size = size
                .checked_add(bytes.len() as u64)
                .ok_or_else(Fault::invalid)?;
            if limit.is_some_and(|limit| limit > 0 && size > limit) {
                return Err(Fault::new(
                    413,
                    "resource_exhausted",
                    "storage object exceeds size limit",
                ));
            }
            file.write_all(&bytes).await.map_err(Fault::internal)?;
            hash.update(bytes);
        }
    }
    file.sync_all().await.map_err(Fault::internal)?;
    drop(file);

    let etag = random_hex::<16>().map_err(Fault::internal)?;
    let mut object = json!({"store": partition.name, "key": key, "size_bytes": size, "content_type": media_type,
        "etag": format!("\"{etag}\""), "sha256": hex(&hash.finalize()), "modified_at": stamp(OffsetDateTime::now_utc())});
    if !partition.tenant.is_empty() {
        object["tenant"] = json!(partition.tenant);
    }
    if let Some(metadata) = metadata {
        object["metadata"] = metadata;
    }
    let record = json!({"kind": "scenery.storage.reference", "schema_revision": reference_revision(),
        "spec_revision": owner.identity["spec_revision"], "producer": owner.identity["producer"], "object": object, "version_id": version});
    let payload = partition.version_path(key, &version);
    let payload_dir = directory(
        &owner.generation,
        payload.parent().expect("payload parent"),
        true,
    )
    .map_err(Fault::internal)?;
    let ref_dir = directory(
        &owner.generation,
        reference.parent().expect("reference parent"),
        true,
    )
    .map_err(Fault::internal)?;
    let mut file = create(&staging, &format!("{version}.ref")).map_err(Fault::internal)?;
    let staged_ref = Stage {
        directory: &staging,
        name: format!("{version}.ref"),
    };
    file.write_all(values::encode(&record).as_bytes())
        .map_err(Fault::internal)?;
    file.sync_all().map_err(Fault::internal)?;
    drop(file);
    let _lock = owner.lock(true).await.map_err(Fault::internal)?;
    let current = partition.current(reference, key).map_err(Fault::internal)?;
    precondition(&parts.headers, current.as_ref())?;
    let payload_name = payload.file_name().expect("payload name");
    match fs::statat(&payload_dir, payload_name, AtFlags::SYMLINK_NOFOLLOW) {
        Err(Errno::NOENT) => {}
        _ => return Err(Fault::internal("immutable storage version already exists")),
    }
    fs::renameat(&staging, stage.name.as_str(), &payload_dir, payload_name)
        .map_err(Fault::internal)?;
    payload_dir.sync_all().map_err(Fault::internal)?;
    staging.sync_all().map_err(Fault::internal)?;
    fs::renameat(
        &staging,
        staged_ref.name.as_str(),
        &ref_dir,
        reference.file_name().expect("reference name"),
    )
    .map_err(Fault::internal)?;
    ref_dir.sync_all().map_err(Fault::internal)?;
    Ok(json_reply(StatusCode::CREATED, &object))
}

fn get_usize(query: &BTreeMap<String, Vec<String>>, key: &str) -> Result<Option<u64>, Fault> {
    query
        .get(key)
        .and_then(|values| values.first())
        .filter(|value| !value.is_empty())
        .map(|value| value.parse().map_err(|_| Fault::invalid()))
        .transpose()
}

fn object_reply(object: &Value) -> Result<Reply, Fault> {
    let mut response = empty(StatusCode::OK);
    let headers = response.headers_mut();
    header(
        headers,
        "x-scenery-storage-object",
        &values::base64url(values::encode(object).as_bytes()),
    );
    if let Some(kind) = object["content_type"].as_str() {
        header(headers, "content-type", kind);
    }
    if let Some(etag) = object["etag"].as_str() {
        header(headers, "etag", etag);
    }
    if let Some(size) = object["size_bytes"].as_u64() {
        header(headers, "content-length", &size.to_string());
    }
    if let Some(metadata) = object.get("metadata") {
        header(
            headers,
            "x-storage-metadata",
            &values::base64url(values::encode(metadata).as_bytes()),
        );
    }
    Ok(response)
}

async fn list(
    partition: &Partition<'_>,
    query: &BTreeMap<String, Vec<String>>,
) -> Result<Reply, Fault> {
    let query_string = |key: &str| {
        query
            .get(key)
            .and_then(|values| values.first())
            .map_or("", String::as_str)
    };
    let prefix = query_string("prefix");
    let delimiter = query_string("delimiter");
    let limit = get_usize(query, "limit")?
        .filter(|limit| *limit != 0)
        .unwrap_or(100)
        .min(1000) as usize;
    if !path_valid(prefix, true) || !matches!(delimiter, "" | "/") {
        return Err(Fault::invalid());
    }
    let owner = partition.owner;
    let binding = digest(
        "list-cursor",
        &[
            &owner.incarnation,
            &owner.generation_id,
            partition.name,
            partition.tenant,
            prefix,
            delimiter,
        ],
    );
    let cursor = query_string("cursor");
    let cursor = if cursor.is_empty() {
        None
    } else {
        if cursor.len() > 12 << 10 {
            return Err(Fault::invalid());
        }
        let cursor = URL_SAFE_NO_PAD
            .decode(cursor)
            .map_err(|_| Fault::invalid())?;
        let cursor = values::parse(&cursor).map_err(|_| Fault::invalid())?;
        if cursor["binding"] != binding
            || !matches!(cursor["type"].as_str(), Some("object" | "prefix"))
        {
            return Err(Fault::invalid());
        }
        Some((
            cursor["last"]
                .as_str()
                .ok_or_else(Fault::invalid)?
                .to_owned(),
            cursor["type"].as_str().unwrap().to_owned(),
        ))
    };
    let _lock = owner.lock(false).await.map_err(Fault::internal)?;
    let path = Path::new("refs").join(&partition.path);
    let dir = match directory(&owner.generation, &path, false) {
        Ok(dir) => dir,
        Err(error) if missing(&error) => {
            return Ok(json_reply(StatusCode::OK, &json!({"objects": []})));
        }
        Err(error) => return Err(Fault::internal(error)),
    };
    let mut entries = BTreeMap::new();
    let mut seen = 0;
    for shard in Dir::read_from(&dir).map_err(Fault::internal)? {
        let shard = shard.map_err(Fault::internal)?;
        let name = shard.file_name().to_str().map_err(Fault::internal)?;
        if matches!(name, "." | "..") {
            continue;
        }
        let shard = directory(&dir, Path::new(name), false).map_err(Fault::internal)?;
        for item in Dir::read_from(&shard).map_err(Fault::internal)? {
            let item = item.map_err(Fault::internal)?;
            let name = item.file_name().to_str().map_err(Fault::internal)?;
            if matches!(name, "." | "..") {
                continue;
            }
            let value = record(&shard, Path::new(name)).map_err(Fault::internal)?;
            partition.check_reference(&value).map_err(Fault::internal)?;
            let mut object = value["object"].clone();
            let key = object["key"].as_str().expect("validated key").to_owned();
            if let Some(suffix) = key.strip_prefix(prefix) {
                let grouping = delimiter == "/" && suffix.contains('/');
                let item = if grouping {
                    (
                        key[..prefix.len() + suffix.find('/').unwrap() + 1].to_owned(),
                        "prefix".to_owned(),
                    )
                } else {
                    (key, "object".to_owned())
                };
                if cursor.as_ref().is_none_or(|cursor| &item > cursor) {
                    object
                        .as_object_mut()
                        .expect("validated object")
                        .remove("metadata");
                    entries.insert(item, object);
                    if entries.len() > limit + 1 {
                        entries.pop_last();
                    }
                }
            }
            seen += 1;
            if seen % 64 == 0 {
                tokio::task::yield_now().await;
            }
        }
    }
    let mut objects = Vec::new();
    let mut prefixes = BTreeSet::new();
    let mut page_bytes = 0;
    let mut last = None;
    let count = entries.len();
    for (index, ((key, kind), object)) in entries.into_iter().enumerate() {
        page_bytes += if kind == "prefix" {
            key.len() * 2 + 8
        } else {
            values::encode(&object).len() + 2
        };
        if index == limit || page_bytes > RECORD_LIMIT - (12 << 10) {
            break;
        }
        if kind == "prefix" {
            prefixes.insert(key.clone());
        } else {
            objects.push(object);
        }
        last = Some((key, kind));
    }
    let length = objects.len() + prefixes.len();
    let mut result = json!({"objects": objects});
    if !prefixes.is_empty() {
        result["prefixes"] = json!(prefixes);
    }
    if length < count
        && let Some((last, kind)) = last
    {
        result["next_cursor"] = json!(values::base64url(
            values::encode(&json!({"binding": binding, "last": last, "type": kind})).as_bytes()
        ));
    }
    Ok(json_reply(StatusCode::OK, &result))
}
