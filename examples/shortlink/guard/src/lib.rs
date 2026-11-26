use std::fmt::Write as _;
use std::io::Cursor;

use scenerycontract::{
    GuardConstructorInput, GuardService, QrSvgInput, QrSvgOutcome, ValidateAndSlugInput,
    ValidateAndSlugOutcome, ValidateResult, sdk,
};

const BASE62: &[u8; 62] = b"0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ";
const BLOCKED_HOSTS: &[&str] = &["localhost", "127.0.0.1", "0.0.0.0", "::1"];

pub struct Service;

pub fn new_service(_input: GuardConstructorInput) -> sdk::Result<Service> {
    Ok(Service)
}

impl GuardService for Service {
    async fn validate_and_slug(
        &self,
        _context: sdk::Context,
        input: ValidateAndSlugInput,
    ) -> sdk::Result<ValidateAndSlugOutcome> {
        let trimmed = input.target_url.trim();
        let (scheme, rest) = match trimmed.split_once("://") {
            Some(parts) => parts,
            None => return Ok(invalid_url("URL must start with http:// or https://.")),
        };
        let scheme_lower = scheme.to_ascii_lowercase();
        if scheme_lower != "http" && scheme_lower != "https" {
            return Ok(invalid_url("Only http and https URLs are supported."));
        }
        let authority = rest.split(['/', '?', '#']).next().unwrap_or("");
        let host = authority
            .rsplit('@')
            .next()
            .unwrap_or("")
            .split(':')
            .next()
            .unwrap_or("")
            .to_ascii_lowercase();
        if host.is_empty() || !host.contains('.') || BLOCKED_HOSTS.contains(&host.as_str()) {
            return Ok(invalid_url(
                "Target URL must contain a valid public hostname.",
            ));
        }

        let normalized_url = format!("{scheme_lower}://{rest}");
        let slug = match input.custom_slug.as_deref().map(str::trim) {
            Some(custom) if !custom.is_empty() => {
                if !is_valid_slug(custom) {
                    return Ok(invalid_url(
                        "Custom slug must be 3-32 characters of letters, digits, '-' or '_'.",
                    ));
                }
                custom.to_owned()
            }
            _ => deterministic_slug(&normalized_url),
        };

        Ok(ValidateAndSlugOutcome::Ok(ValidateResult {
            slug,
            normalized_url,
            host,
        }))
    }

    async fn qr_svg(
        &self,
        context: sdk::Context,
        input: QrSvgInput,
    ) -> sdk::Result<(QrSvgOutcome, Option<sdk::Stream>)> {
        let slug = input.slug.trim();
        if !is_valid_slug(slug) {
            let problem = sdk::serde_json::json!({
                "code": "invalid_slug",
                "message": "Slug contains invalid characters."
            });
            return Ok((
                QrSvgOutcome::InvalidSlug(sdk::serde_json::value::to_raw_value(&problem)?),
                None,
            ));
        }

        let key = format!("qr/{slug}.svg");
        let bytes = match read_cached_qr(&context, &key).await {
            Some(cached) => cached,
            None => {
                let svg = render_qr_svg(slug).into_bytes();
                let _ = context
                    .storage_write(
                        "assets",
                        &key,
                        Cursor::new(svg.clone()),
                        sdk::StorageWriteOptions {
                            content_type: Some("image/svg+xml"),
                            ..Default::default()
                        },
                    )
                    .await;
                svg
            }
        };

        let size = bytes.len() as u64;
        let outcome = QrSvgOutcome::Ok(sdk::ContractBytes(String::new()));
        Ok((
            outcome,
            Some(sdk::Stream {
                reader: Box::pin(Cursor::new(bytes)),
                size,
            }),
        ))
    }
}

async fn read_cached_qr(context: &sdk::Context, key: &str) -> Option<Vec<u8>> {
    let mut reader = context.storage_read("assets", key).await.ok()?;
    let mut out = Vec::new();
    while let Ok(Some(chunk)) = reader.next_chunk().await {
        out.extend_from_slice(&chunk);
    }
    if out.is_empty() { None } else { Some(out) }
}

fn invalid_url(message: &str) -> ValidateAndSlugOutcome {
    let problem = sdk::serde_json::json!({
        "code": "invalid_url",
        "message": message,
    });
    ValidateAndSlugOutcome::InvalidUrl(
        sdk::serde_json::value::to_raw_value(&problem).expect("valid problem json"),
    )
}

fn is_valid_slug(slug: &str) -> bool {
    (3..=32).contains(&slug.len())
        && slug
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
}

/// Generates a compact 7-character base62 slug via FNV-1a hashing.
fn deterministic_slug(url: &str) -> String {
    let mut hash: u64 = 0xcbf29ce484222325;
    for byte in url.bytes() {
        hash ^= u64::from(byte);
        hash = hash.wrapping_mul(0x100000001b3);
    }
    let mut out = String::with_capacity(7);
    let mut n = hash;
    for _ in 0..7 {
        out.push(BASE62[(n % 62) as usize] as char);
        n /= 62;
    }
    out
}

/// Renders a deterministic 21x21 QR-style SVG matrix for the given slug.
fn render_qr_svg(slug: &str) -> String {
    const N: usize = 21;
    let mut grid = [[false; N]; N];

    // Three standard corner finder patterns.
    for &(ox, oy) in &[(0usize, 0usize), (N - 7, 0), (0, N - 7)] {
        for y in 0..7 {
            for x in 0..7 {
                let border = x == 0 || x == 6 || y == 0 || y == 6;
                let center = (2..=4).contains(&x) && (2..=4).contains(&y);
                grid[oy + y][ox + x] = border || center;
            }
        }
    }

    // Fill data modules deterministically from slug bytes.
    let mut state: u64 = 0x9e3779b97f4a7c15;
    for b in slug.bytes() {
        state ^= u64::from(b);
        state = state.wrapping_mul(0xbf58476d1ce4e5b9);
        state ^= state >> 31;
    }
    for (y, row) in grid.iter_mut().enumerate() {
        for (x, cell) in row.iter_mut().enumerate() {
            let in_finder = (x < 8 && y < 8) || (x >= N - 8 && y < 8) || (x < 8 && y >= N - 8);
            if in_finder {
                continue;
            }
            state ^= state << 13;
            state ^= state >> 7;
            state ^= state << 17;
            *cell = (state & 1) == 1;
        }
    }

    let size = (N + 4) * 8;
    let mut svg = String::with_capacity(2048);
    let _ = write!(
        svg,
        r#"<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {size} {size}" width="{size}" height="{size}" role="img" aria-label="QR code for {slug}">"#,
    );
    svg.push_str(r##"<rect width="100%" height="100%" rx="12" fill="#ffffff"/>"##);
    for (y, row) in grid.iter().enumerate() {
        for (x, &dark) in row.iter().enumerate() {
            if dark {
                let px = (x + 2) * 8;
                let py = (y + 2) * 8;
                let _ = write!(
                    svg,
                    r##"<rect x="{px}" y="{py}" width="8" height="8" rx="1.5" fill="#111827"/>"##
                );
            }
        }
    }
    svg.push_str("</svg>");
    svg
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn slug_generation_is_deterministic_and_valid() {
        let slug = deterministic_slug("https://example.com/docs");
        assert_eq!(slug, deterministic_slug("https://example.com/docs"));
        assert!(is_valid_slug(&slug));
    }

    #[test]
    fn qr_svg_contains_valid_svg_root() {
        let svg = render_qr_svg("abc1234");
        assert!(svg.starts_with("<svg "));
        assert!(svg.ends_with("</svg>"));
    }
}
