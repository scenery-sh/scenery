use argon2::password_hash::{PasswordHash, PasswordHasher, PasswordVerifier, SaltString};
use argon2::{Algorithm, Argon2, Params, Version};
use serde_json::{Value, json};
use subtle::ConstantTimeEq;
use tokio_postgres::{Client, GenericClient, Row, Transaction, error::SqlState};

use crate::auth::Claims;
use crate::browser_auth::{
    AuthResult, Browser, audit, consume_once, denied, email, invalid, issue_once, ok, precondition,
    random, safe_redirect, text, token_hash, unauthorized, uuid,
};
use crate::host::{Fault, Reply};

static PASSWORD_SLOTS: tokio::sync::Semaphore = tokio::sync::Semaphore::const_new(2);

pub(crate) const SESSION_TTL: u64 = 30 * 86400;

pub(crate) async fn signup(
    browser: &Browser<'_>,
    db: &mut Client,
    input: &Value,
) -> AuthResult<Reply> {
    let address = text(input, "email");
    let normalized = email(address)?;
    let password = input["password"].as_str().unwrap_or_default();
    if !(10..=1024).contains(&password.len()) {
        return Err(invalid("password must contain between 10 and 1024 bytes"));
    }
    rate_limit(browser, db, "signup_email", &normalized).await?;
    let password = password.to_owned();
    let permit = PASSWORD_SLOTS.acquire().await.map_err(Fault::internal)?;
    let hash = tokio::task::spawn_blocking(move || {
        let _permit = permit;
        hash_password(&password)
    })
    .await
    .map_err(Fault::internal)??;
    let tx = db.transaction().await.map_err(Fault::internal)?;
    let user = tx.query_one("INSERT INTO scenery.scenery_auth_users (id,primary_email,normalized_primary_email,display_name)
        VALUES (gen_random_uuid(),$1,$2,$3) RETURNING id::text",
        &[&address,&normalized,&display_name(&normalized,text(input,"display_name"))]).await.map_err(duplicate_email)?;
    let user: String = user.get(0);
    tx.execute("INSERT INTO scenery.scenery_auth_auth_identities (id,user_id,provider,provider_subject,email,normalized_email,password_hash)
        VALUES(gen_random_uuid(),$1::text::uuid,'email',$2,$3,$2,$4)", &[&user,&normalized,&address,&hash]).await.map_err(duplicate_email)?;
    let claims = Claims {
        user,
        ..Claims::default()
    };
    let token = issue_once(
        &tx,
        "email_verification",
        &claims,
        address,
        &normalized,
        json!({"redirect_path":safe_redirect(text(input,"redirect_path"))}),
        86400,
    )
    .await?;
    audit(&tx, browser, "signup_email", &claims, &json!({})).await?;
    tx.commit().await.map_err(Fault::internal)?;
    let mut reply = json!({"requires_email_verification":true, "email":address});
    if browser.host.development {
        reply["dev_verification_token"] = json!(token);
    }
    Ok(ok(&reply))
}

pub(crate) async fn verify_email(
    browser: &Browser<'_>,
    db: &mut Client,
    input: &Value,
) -> AuthResult<Reply> {
    let token = text(input, "token");
    if token.is_empty() {
        return Err(invalid("token is required"));
    }
    let tx = db.transaction().await.map_err(Fault::internal)?;
    let consumed = consume_once(&tx, "email_verification", token).await?;
    let user: String = consumed.get(0);
    tx.execute("UPDATE scenery.scenery_auth_users SET email_verified_at=COALESCE(email_verified_at,now()), updated_at=now() WHERE id=$1::text::uuid",
        &[&user]).await.map_err(Fault::internal)?;
    let reply = sign_in(browser, &tx, &user, "email_verified").await?;
    tx.commit().await.map_err(Fault::internal)?;
    Ok(reply)
}

pub(crate) async fn login(
    browser: &Browser<'_>,
    db: &mut Client,
    input: &Value,
) -> AuthResult<Reply> {
    let normalized = email(text(input, "email"))?;
    rate_limit(browser, db, "login_email", &normalized).await?;
    let wrong = || unauthorized("email or password is invalid");
    let password = input["password"].as_str().unwrap_or_default().to_owned();
    if password.len() > 1024 {
        return Err(wrong());
    }
    let identity = db
        .query_opt(
            "SELECT user_id::text,password_hash,id::text FROM scenery.scenery_auth_auth_identities
        WHERE provider='email' AND provider_subject=$1",
            &[&normalized],
        )
        .await
        .map_err(Fault::internal)?
        .ok_or_else(wrong)?;
    let hash: String = identity.get(1);
    let permit = PASSWORD_SLOTS.acquire().await.map_err(Fault::internal)?;
    let upgrade = tokio::task::spawn_blocking(move || {
        let _permit = permit;
        password_check(&password, &hash)
    })
    .await
    .map_err(Fault::internal)??;
    let tx = db.transaction().await.map_err(Fault::internal)?;
    if let Some(updated) = upgrade {
        tx.execute("UPDATE scenery.scenery_auth_auth_identities SET password_hash=$2,updated_at=now() WHERE id=$1::text::uuid",
            &[&identity.get::<_,String>(2),&updated]).await.map_err(Fault::internal)?;
    }
    let reply = sign_in(browser, &tx, &identity.get::<_, String>(0), "login_email").await?;
    tx.commit().await.map_err(Fault::internal)?;
    Ok(reply)
}

pub(crate) async fn dev_email_session(
    browser: &Browser<'_>,
    db: &mut Client,
    address: &str,
    tenant: &str,
) -> AuthResult<Reply> {
    let normalized = email(address)?;
    let tenant = uuid(tenant, "tenant_id is invalid")?;
    let tx = db.transaction().await.map_err(Fault::internal)?;
    let user = tx.query_one(
        "INSERT INTO scenery.scenery_auth_users (id,primary_email,normalized_primary_email,display_name,email_verified_at)
         VALUES (gen_random_uuid(),$1,$2,$3,now())
         ON CONFLICT (normalized_primary_email) WHERE normalized_primary_email <> ''
         DO UPDATE SET normalized_primary_email=EXCLUDED.normalized_primary_email
         RETURNING id::text,disabled_at IS NOT NULL",
        &[&address, &normalized, &display_name(&normalized, "")],
    ).await.map_err(Fault::internal)?;
    if user.get::<_, bool>(1) {
        return Err(denied("default user is disabled"));
    }
    let user: String = user.get(0);
    let workspace = tx
        .query_one(
            "INSERT INTO scenery.scenery_auth_tenants (id,name)
         VALUES ($1::text::uuid,'Development Workspace')
         ON CONFLICT (id) DO UPDATE SET name=scenery.scenery_auth_tenants.name
         RETURNING id::text,deleted_at IS NOT NULL",
            &[&tenant],
        )
        .await
        .map_err(Fault::internal)?;
    if workspace.get::<_, bool>(1) {
        return Err(precondition("default tenant is deleted"));
    }
    let tenant: String = workspace.get(0);
    tx.execute(
        "INSERT INTO scenery.scenery_auth_organization_memberships (id,tenant_id,user_id,role)
         VALUES (gen_random_uuid(),$1::text::uuid,$2::text::uuid,'owner')
         ON CONFLICT (user_id,tenant_id) WHERE disabled_at IS NULL DO NOTHING",
        &[&tenant, &user],
    )
    .await
    .map_err(Fault::internal)?;
    let mut claims = Claims {
        user,
        tenant,
        ..Claims::default()
    };
    let reply = create_session(browser, &tx, &mut claims, "", SESSION_TTL).await?;
    tx.commit().await.map_err(Fault::internal)?;
    Ok(reply)
}

pub(crate) async fn sign_in(
    browser: &Browser<'_>,
    tx: &Transaction<'_>,
    user: &str,
    event: &str,
) -> AuthResult<Reply> {
    let tenant = active_tenant(tx, user, "").await?;
    let mut claims = Claims {
        user: user.into(),
        tenant,
        ..Claims::default()
    };
    let reply = create_session(browser, tx, &mut claims, "", SESSION_TTL).await?;
    audit(tx, browser, event, &claims, &json!({})).await?;
    Ok(reply)
}

pub(crate) async fn refresh(browser: &Browser<'_>, db: &mut Client) -> AuthResult<Reply> {
    let raw = browser.refresh_token();
    let id = session_id(raw).ok_or_else(|| {
        unauthorized(if raw.is_empty() {
            "refresh session is missing"
        } else {
            "refresh session is invalid"
        })
    })?;
    let tx = db.transaction().await.map_err(Fault::internal)?;
    let session = tx
        .query_opt(
            "SELECT user_id::text, COALESCE(active_tenant_id::text,''),
        COALESCE(actor_user_id::text,''), COALESCE(impersonation_id::text,''), impersonation_reason,
        (revoked_at IS NULL AND expires_at>now()), token_hash,
        CASE WHEN previous_token_expires_at>now() THEN previous_token_hash ELSE '' END,
        GREATEST(0, FLOOR(EXTRACT(EPOCH FROM expires_at-now())))::bigint
        FROM scenery.scenery_auth_refresh_sessions WHERE id=$1::text::uuid FOR UPDATE",
            &[&id],
        )
        .await
        .map_err(Fault::internal)?
        .ok_or_else(|| unauthorized("refresh session is invalid"))?;
    if !session.get::<_, bool>(5) {
        return Err(unauthorized("refresh session is expired"));
    }
    let hash = token_hash(raw);
    let matching = [session.get::<_, String>(6), session.get(7)]
        .iter()
        .any(|expected| bool::from(expected.as_bytes().ct_eq(hash.as_bytes())));
    if !matching {
        revoke(&tx, id, "refresh_replay").await?;
        tx.commit().await.map_err(Fault::internal)?;
        return Err(unauthorized("refresh session is invalid"));
    }
    let mut claims = Claims {
        user: session.get(0),
        tenant: session.get(1),
        session: id.into(),
        actor: session.get(2),
        impersonation_id: session.get(3),
        ..Claims::default()
    };
    if claims.actor.is_empty() {
        claims.tenant = active_tenant(&tx, &claims.user, &claims.tenant).await?;
    } else {
        let actor = user(&tx, &claims.actor).await?;
        if actor.get::<_, bool>("disabled") || !actor.get::<_, bool>("can_impersonate_users") {
            return Err(denied("platform impersonation permission is required"));
        }
    }
    let next = format!("{id}.{}", random::<32>()?);
    tx.execute("UPDATE scenery.scenery_auth_refresh_sessions SET previous_token_hash=token_hash,
        previous_token_expires_at=now()+interval '30 seconds', token_hash=$2, active_tenant_id=$3::text::uuid,
        rotated_at=now(), updated_at=now() WHERE id=$1::text::uuid", &[&id,&token_hash(&next),&claims.tenant])
        .await.map_err(Fault::internal)?;
    let data = bootstrap(browser, &tx, &claims, session.get(4)).await?;
    audit(&tx, browser, "refresh", &claims, &json!({})).await?;
    tx.commit().await.map_err(Fault::internal)?;
    let mut reply = ok(&data);
    browser.set_cookie(&mut reply, &next, session.get::<_, i64>(8) as u64);
    Ok(reply)
}

pub(crate) async fn logout(browser: &Browser<'_>, db: &Client) -> AuthResult<Reply> {
    let raw = browser.refresh_token();
    if let Some(id) = session_id(raw) {
        db.execute("UPDATE scenery.scenery_auth_refresh_sessions SET revoked_at=now(),revoked_reason='logout',updated_at=now()
            WHERE id=$1::text::uuid AND revoked_at IS NULL AND (token_hash=$2 OR
                 (previous_token_hash=$2 AND previous_token_expires_at>now()))", &[&id,&token_hash(raw)])
            .await.map_err(Fault::internal)?;
    }
    let mut reply = ok(&json!({"ok":true}));
    browser.set_cookie(&mut reply, "", 0);
    Ok(reply)
}

pub(crate) async fn me(browser: &Browser<'_>, db: &Client, claims: &Claims) -> AuthResult<Reply> {
    let reason = impersonation_reason(db, claims).await?;
    Ok(ok(&bootstrap(browser, db, claims, &reason).await?))
}

pub(crate) async fn impersonation_reason(
    db: &impl GenericClient,
    claims: &Claims,
) -> AuthResult<String> {
    if claims.actor.is_empty() {
        return Ok(String::new());
    }
    let value = db.query_one("SELECT impersonation_reason FROM scenery.scenery_auth_refresh_sessions WHERE id=$1::text::uuid",
        &[&claims.session]).await.map_err(Fault::internal)?;
    Ok(value.get(0))
}

pub(crate) async fn revoke(db: &impl GenericClient, id: &str, reason: &str) -> AuthResult<()> {
    db.execute("UPDATE scenery.scenery_auth_refresh_sessions SET revoked_at=now(),revoked_reason=$2,updated_at=now()
        WHERE id=$1::text::uuid AND revoked_at IS NULL", &[&id,&reason]).await.map_err(Fault::internal)?;
    Ok(())
}

pub(crate) async fn create_session(
    browser: &Browser<'_>,
    db: &impl GenericClient,
    claims: &mut Claims,
    reason: &str,
    ttl: u64,
) -> AuthResult<Reply> {
    claims.session = db
        .query_one("SELECT gen_random_uuid()::text", &[])
        .await
        .map_err(Fault::internal)?
        .get(0);
    let raw = format!("{}.{}", claims.session, random::<32>()?);
    db.execute("INSERT INTO scenery.scenery_auth_refresh_sessions (id,user_id,active_tenant_id,token_hash,
        expires_at,user_agent,ip_hash,actor_user_id,impersonation_id,impersonation_reason)
        VALUES ($1::text::uuid,$2::text::uuid,$3::text::uuid,$4,now()+make_interval(secs => $5::int),
            $6,$7,NULLIF($8,'')::uuid,NULLIF($9,'')::uuid,$10)",
        &[&claims.session,&claims.user,&claims.tenant,&token_hash(&raw),&(ttl as i32),&browser.user_agent(),
            &browser.ip_hash(),&claims.actor,&claims.impersonation_id,&reason]).await.map_err(Fault::internal)?;
    let mut reply = ok(&bootstrap(browser, db, claims, reason).await?);
    browser.set_cookie(&mut reply, &raw, ttl);
    Ok(reply)
}

pub(crate) async fn user(db: &impl GenericClient, id: &str) -> AuthResult<Row> {
    db.query_opt("SELECT id::text, primary_email, normalized_primary_email, display_name, avatar_url,
        email_verified_at IS NOT NULL AS verified, disabled_at IS NOT NULL AS disabled, can_impersonate_users
        FROM scenery.scenery_auth_users WHERE id=$1::text::uuid", &[&id]).await.map_err(Fault::internal)?
        .ok_or_else(|| unauthorized("unknown user"))
}

async fn memberships(db: &impl GenericClient, user: &str) -> AuthResult<Vec<Row>> {
    db.query(
        "SELECT t.id::text,t.name,m.role
        FROM scenery.scenery_auth_organization_memberships m
        JOIN scenery.scenery_auth_tenants t ON t.id=m.tenant_id
        WHERE m.user_id=$1::text::uuid AND m.disabled_at IS NULL AND t.deleted_at IS NULL
        ORDER BY lower(t.name),t.name,m.tenant_id",
        &[&user],
    )
    .await
    .map_err(Fault::internal)
}

pub(crate) async fn bootstrap(
    browser: &Browser<'_>,
    db: &impl GenericClient,
    claims: &Claims,
    reason: &str,
) -> AuthResult<Value> {
    let user = user(db, &claims.user).await?;
    if user.get::<_, bool>("disabled") {
        return Err(denied("user is disabled"));
    }
    let orgs = memberships(db, &claims.user).await?;
    if !orgs
        .iter()
        .any(|org| org.get::<_, &str>(0) == claims.tenant)
    {
        return Err(denied("workspace access is disabled"));
    }
    let organizations: Vec<_> = orgs.iter().map(|org| json!({
        "tenant_id":org.get::<_,&str>(0), "tenant_name":org.get::<_,&str>(1).trim(), "role":org.get::<_,&str>(2)
    })).collect();
    let impersonation = if claims.actor.is_empty() {
        json!({})
    } else {
        json!({"actor_user_id":claims.actor, "impersonation_id":claims.impersonation_id, "reason":reason})
    };
    let token = browser
        .host
        .auth
        .issue(claims.clone())
        .map_err(Fault::internal)?;
    Ok(
        json!({"token":token, "current_tenant_id":claims.tenant, "organizations":organizations,
            "impersonation":impersonation, "user":{
                "id":claims.user, "email":user.get::<_,&str>("primary_email").trim(),
                "display_name":user.get::<_,&str>("display_name").trim(), "avatar_url":user.get::<_,&str>("avatar_url").trim(),
                "email_verified":user.get::<_,bool>("verified"), "can_impersonate_users":user.get::<_,bool>("can_impersonate_users")
            }
        }),
    )
}

pub(crate) async fn active_tenant(
    tx: &Transaction<'_>,
    id: &str,
    preferred: &str,
) -> AuthResult<String> {
    // Serialize personal workspace creation across simultaneous logins.
    tx.execute(
        "SELECT pg_advisory_xact_lock(hashtextextended($1, 732))",
        &[&id],
    )
    .await
    .map_err(Fault::internal)?;
    let user = user(tx, id).await?;
    if !user.get::<_, bool>("verified") {
        return Err(precondition("email verification is required"));
    }
    if user.get::<_, bool>("disabled") {
        return Err(denied("user is disabled"));
    }
    let orgs = memberships(tx, id).await?;
    if let Some(org) = orgs
        .iter()
        .find(|row| row.get::<_, &str>(0) == preferred)
        .or_else(|| orgs.first())
    {
        return Ok(org.get(0));
    }
    let name = user.get::<_, &str>("display_name").trim();
    let name = if name.is_empty() || name.eq_ignore_ascii_case("my") {
        "My"
    } else {
        name
    };
    let tenant: String = tx.query_one("INSERT INTO scenery.scenery_auth_tenants (id,name) VALUES(gen_random_uuid(),$1) RETURNING id::text",
        &[&format!("{name} Workspace")]).await.map_err(Fault::internal)?.get(0);
    tx.execute(
        "INSERT INTO scenery.scenery_auth_organization_memberships (id,tenant_id,user_id,role)
        VALUES(gen_random_uuid(),$1::text::uuid,$2::text::uuid,'owner')",
        &[&tenant, &id],
    )
    .await
    .map_err(Fault::internal)?;
    Ok(tenant)
}

pub(crate) fn display_name<'a>(email: &'a str, explicit: &'a str) -> &'a str {
    if explicit.is_empty() {
        email.split_once('@').map_or("User", |(name, _)| name)
    } else {
        explicit
    }
}

fn session_id(token: &str) -> Option<&str> {
    let (id, secret) = token.split_once('.')?;
    (!secret.is_empty()).then_some(())?;
    uuid(id, "invalid session id").ok()
}

fn duplicate_email(error: tokio_postgres::Error) -> Fault {
    if error.code() == Some(&SqlState::UNIQUE_VIOLATION) {
        Fault::new(409, "already_exists", "email is already registered")
    } else {
        Fault::internal(error)
    }
}

async fn rate_limit(
    browser: &Browser<'_>,
    db: &Client,
    purpose: &str,
    email: &str,
) -> AuthResult<()> {
    let count: i32 = db.query_one("INSERT INTO scenery.scenery_auth_auth_attempts AS attempts (id,purpose,normalized_email,ip_hash,attempt_count)
        VALUES(gen_random_uuid(),$1,$2,$3,1) ON CONFLICT(purpose,normalized_email,ip_hash)
        DO UPDATE SET attempt_count=CASE WHEN attempts.window_started_at<now()-interval '15 minutes' THEN 1 ELSE attempts.attempt_count+1 END,
        window_started_at=CASE WHEN attempts.window_started_at<now()-interval '15 minutes' THEN now() ELSE attempts.window_started_at END,
        last_attempt_at=now() RETURNING attempt_count", &[&purpose,&email,&browser.ip_hash()]).await.map_err(Fault::internal)?.get(0);
    if count > 20 {
        return Err(Fault::new(
            429,
            "resource_exhausted",
            "too many auth attempts; try again later",
        ));
    }
    Ok(())
}

fn hash_password(password: &str) -> AuthResult<String> {
    let mut salt = [0u8; 16];
    getrandom::fill(&mut salt).map_err(Fault::internal)?;
    let salt = SaltString::encode_b64(&salt).map_err(Fault::internal)?;
    let params = Params::new(65536, 2, 1, Some(32)).map_err(Fault::internal)?;
    let hash = Argon2::new(Algorithm::Argon2id, Version::V0x13, params)
        .hash_password(password.as_bytes(), &salt)
        .map_err(Fault::internal)?;
    Ok(hash.to_string())
}

fn password_check(password: &str, digest: &str) -> AuthResult<Option<String>> {
    let wrong = || unauthorized("email or password is invalid");
    let digest = PasswordHash::new(digest).map_err(|_| wrong())?;
    let parameters = Params::try_from(&digest).map_err(|_| wrong())?;
    if digest.algorithm.as_str() != "argon2id"
        || digest.version != Some(19)
        || parameters.m_cost() > 262144
        || parameters.t_cost() > 8
        || parameters.p_cost() > 8
    {
        return Err(wrong());
    }
    Argon2::default()
        .verify_password(password.as_bytes(), &digest)
        .map_err(|_| wrong())?;
    let standard = parameters.m_cost() == 65536
        && parameters.t_cost() == 2
        && parameters.p_cost() == 1
        && digest.hash.is_some_and(|hash| hash.len() == 32);
    if standard || password.len() < 10 {
        Ok(None)
    } else {
        hash_password(password).map(Some)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn password_format_and_verification() {
        let hash = hash_password("sample password").unwrap_or_else(|_| panic!("password hash"));
        assert!(hash.starts_with("$argon2id$v=19$m=65536,t=2,p=1$"));
        assert!(matches!(password_check("sample password", &hash), Ok(None)));
        assert!(password_check("wrong password", &hash).is_err());
    }
}
