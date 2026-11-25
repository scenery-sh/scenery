use std::io::Read;
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::path::Path;

use base64::{Engine, engine::general_purpose};
use ring::aead::{AES_256_GCM, Aad, LessSafeKey, Nonce, UnboundKey};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use time::{Duration, OffsetDateTime, format_description::well_known::Rfc3339};

use crate::process::{hex, random_hex};

const KEY_ID: &[u8] = b"runtime";
const MAX_TOKEN_BYTES: usize = 4096;
const TTL: Duration = Duration::hours(24);

pub(crate) struct Key(LessSafeKey);

#[derive(Serialize, Deserialize)]
pub(crate) struct Claims<T> {
    version: u32,
    pub assistant_address: String,
    pub nonce: String,
    pub owner_digest: String,
    pub conversation_digest: String,
    #[serde(with = "time::serde::rfc3339")]
    pub issued_at: OffsetDateTime,
    #[serde(with = "time::serde::rfc3339")]
    pub expires_at: OffsetDateTime,
    #[serde(flatten)]
    pub data: T,
}

#[derive(Serialize, Deserialize)]
pub(crate) struct Conversation {
    pub private_session_id: String,
    pub continuation_token: String,
}

#[derive(Serialize, Deserialize)]
pub(crate) struct Approval {
    pub run_id: String,
    pub approval_id: String,
    pub decision_context: String,
}

pub(crate) trait Handle: Serialize + for<'de> Deserialize<'de> {
    const PREFIX: &'static str;
    const AAD: &'static [u8];
    fn valid(&self) -> bool;
}

impl Handle for Conversation {
    const PREFIX: &str = "conv1_";
    const AAD: &[u8] = b"scenery.assistant-token/conversation/v1\0runtime";
    fn valid(&self) -> bool {
        valid_string(&self.private_session_id) && valid_string(&self.continuation_token)
    }
}

impl Handle for Approval {
    const PREFIX: &str = "appr1_";
    const AAD: &[u8] = b"scenery.assistant-token/approval/v1\0runtime";
    fn valid(&self) -> bool {
        valid_string(&self.run_id)
            && valid_string(&self.approval_id)
            && valid_string(&self.decision_context)
    }
}

fn valid_string(value: &str) -> bool {
    !value.is_empty() && value.len() <= 1024 && !value.contains('\0')
}

impl Key {
    pub(crate) fn load(file: Option<&Path>) -> Option<Self> {
        let value = match std::env::var("SCENERY_ASSISTANT_TOKEN_KEY")
            .ok()
            .filter(|value| !value.trim().is_empty())
        {
            Some(value) => value,
            None => read_private(file?, 4096)?,
        };
        let value = value.trim();
        let key = if value.len() == 32 {
            value.as_bytes().to_vec()
        } else {
            decode_hex(value)
                .filter(|bytes| bytes.len() == 32)
                .or_else(|| {
                    [
                        general_purpose::STANDARD_NO_PAD,
                        general_purpose::STANDARD,
                        general_purpose::URL_SAFE_NO_PAD,
                        general_purpose::URL_SAFE,
                    ]
                    .iter()
                    .find_map(|engine| engine.decode(value).ok().filter(|bytes| bytes.len() == 32))
                })?
        };
        Some(Self(LessSafeKey::new(
            UnboundKey::new(&AES_256_GCM, &key).ok()?,
        )))
    }

    pub(crate) fn seal<T: Handle>(
        &self,
        assistant: &str,
        owner: &str,
        conversation: &str,
        data: T,
    ) -> Option<String> {
        let now = OffsetDateTime::now_utc();
        let claims = Claims {
            version: 1,
            assistant_address: assistant.into(),
            nonce: random_hex::<16>().ok()?,
            owner_digest: owner.into(),
            conversation_digest: conversation.into(),
            issued_at: now,
            expires_at: now + TTL,
            data,
        };
        if !claims.data.valid()
            || !valid_string(conversation)
            || !valid_string(assistant)
            || !valid_string(owner)
        {
            return None;
        }
        let mut payload = serde_json::to_vec(&claims).ok()?;
        let mut nonce = [0; 12];
        getrandom::fill(&mut nonce).ok()?;
        self.0
            .seal_in_place_append_tag(
                Nonce::assume_unique_for_key(nonce),
                Aad::from(T::AAD),
                &mut payload,
            )
            .ok()?;
        let mut envelope = vec![0, KEY_ID.len() as u8];
        envelope.extend_from_slice(KEY_ID);
        envelope.extend_from_slice(&nonce);
        envelope.extend(payload);
        (envelope.len() <= MAX_TOKEN_BYTES).then(|| format!("{}{}", T::PREFIX, hex(&envelope)))
    }

    pub(crate) fn open<T: Handle>(
        &self,
        token: &str,
        assistant: &str,
        owner: &str,
    ) -> Option<Claims<T>> {
        let encoded = token.strip_prefix(T::PREFIX)?;
        if encoded.len() > 2 * MAX_TOKEN_BYTES {
            return None;
        }
        let mut payload = decode_hex(encoded)?;
        payload
            .strip_prefix(&[0, KEY_ID.len() as u8])?
            .strip_prefix(KEY_ID)?;
        let payload = &mut payload[2 + KEY_ID.len()..];
        let (nonce, ciphertext) = payload.split_at_mut_checked(12)?;
        let clear = self
            .0
            .open_in_place(
                Nonce::try_assume_unique_for_key(nonce).ok()?,
                Aad::from(T::AAD),
                ciphertext,
            )
            .ok()?;
        let claims: Claims<T> = serde_json::from_slice(clear).ok()?;
        let now = OffsetDateTime::now_utc();
        (claims.version == 1
            && claims.assistant_address == assistant
            && claims.owner_digest == owner
            && claims.expires_at > now
            && claims.expires_at > claims.issued_at
            && claims.expires_at <= claims.issued_at + TTL
            && claims.issued_at <= now + Duration::minutes(5)
            && claims.nonce.len() == 32
            && decode_hex(&claims.nonce).is_some()
            && valid_string(&claims.conversation_digest)
            && claims.data.valid())
        .then_some(claims)
    }
}

pub(crate) fn read_private(path: &Path, limit: u64) -> Option<String> {
    let file = std::fs::OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK)
        .open(path)
        .ok()?;
    let info = file.metadata().ok()?;
    if !info.is_file() || info.permissions().mode() & 0o077 != 0 {
        return None;
    }
    let mut text = String::new();
    file.take(limit + 1).read_to_string(&mut text).ok()?;
    (text.len() as u64 <= limit).then_some(text)
}

fn decode_hex(value: &str) -> Option<Vec<u8>> {
    if !value.len().is_multiple_of(2) || value.as_bytes().iter().any(u8::is_ascii_uppercase) {
        return None;
    }
    value
        .as_bytes()
        .as_chunks::<2>()
        .0
        .iter()
        .map(|bytes| {
            let high = (bytes[0] as char).to_digit(16)?;
            let low = (bytes[1] as char).to_digit(16)?;
            Some((high * 16 + low) as u8)
        })
        .collect()
}

fn digest(domain: &[u8], identity: &str) -> String {
    let mut hash = Sha256::new();
    hash.update(domain);
    hash.update(identity.as_bytes());
    format!("sha256:{}", hex(&hash.finalize()))
}

pub(crate) fn owner(principal: &str) -> String {
    digest(b"scenery.assistant-owner\0", principal)
}

pub(crate) fn conversation(id: &str) -> String {
    digest(b"scenery.assistant-conversation\0", id)
}

pub(crate) fn valid_run(id: &str) -> bool {
    id.strip_prefix("run_")
        .is_some_and(|hex| !hex.is_empty() && hex.len() <= 8192 && decode_hex(hex).is_some())
}

pub(crate) fn valid_time(value: &str) -> bool {
    OffsetDateTime::parse(value, &Rfc3339).is_ok()
}
