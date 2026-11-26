use serde_json::{Value, json};
use tokio_postgres::{Client, GenericClient, Transaction};

use crate::auth::Claims;
use crate::auth_sessions::{
    SESSION_TTL, active_tenant, bootstrap, create_session, impersonation_reason, revoke, user,
};
use crate::browser_auth::{
    AuthResult, Browser, Route, audit, consume_once, denied, email, invalid, issue_once, ok,
    precondition, text, uuid,
};
use crate::host::{Fault, Reply};

pub(crate) async fn serve(
    browser: &Browser<'_>,
    db: &mut Client,
    claims: &Claims,
    route: Route<'_>,
    input: &Value,
) -> AuthResult<Reply> {
    let tx = db.transaction().await.map_err(Fault::internal)?;
    let result = match route {
        Route::Switch => switch(browser, &tx, claims, text(input, "tenant_id")).await?,
        Route::Members(tenant) => {
            let tenant = uuid(tenant, "tenant_id is invalid")?;
            require_member(&tx, &claims.user, tenant).await?;
            members(&tx, tenant).await?
        }
        Route::Invite(tenant) => invite(browser, &tx, claims, tenant, input).await?,
        Route::Accept => accept(browser, &tx, claims, text(input, "token")).await?,
        Route::Role(tenant, user) => {
            change_member(&tx, claims, tenant, user, Some(text(input, "role"))).await?
        }
        Route::Disable(tenant) => {
            change_member(&tx, claims, tenant, text(input, "user_id"), None).await?
        }
        Route::Impersonate => start_impersonation(browser, &tx, claims, input).await?,
        Route::StopImpersonation => stop_impersonation(browser, &tx, claims).await?,
        _ => return Err(invalid("invalid account operation")),
    };
    tx.commit().await.map_err(Fault::internal)?;
    Ok(result)
}

async fn switch(
    browser: &Browser<'_>,
    tx: &Transaction<'_>,
    claims: &Claims,
    tenant: &str,
) -> AuthResult<Reply> {
    let tenant = uuid(tenant, "tenant_id is invalid")?;
    require_member(tx, &claims.user, tenant).await?;
    let mut claims = claims.clone();
    set_tenant(tx, &mut claims, tenant).await?;
    let reason = impersonation_reason(tx, &claims).await?;
    let data = bootstrap(browser, tx, &claims, &reason).await?;
    audit(tx, browser, "organization_switched", &claims, &json!({})).await?;
    Ok(ok(&data))
}

async fn require_member(db: &impl GenericClient, user: &str, tenant: &str) -> AuthResult<String> {
    let record = db.query_opt("SELECT m.role FROM scenery.scenery_auth_organization_memberships m
        JOIN scenery.scenery_auth_tenants t ON t.id=m.tenant_id
        WHERE m.tenant_id=$1::text::uuid AND m.user_id=$2::text::uuid AND m.disabled_at IS NULL AND t.deleted_at IS NULL",
        &[&tenant,&user]).await.map_err(Fault::internal)?
        .ok_or_else(|| denied("workspace access is disabled"))?;
    Ok(record.get(0))
}

async fn require_owner(tx: &Transaction<'_>, user: &str, tenant: &str) -> AuthResult<()> {
    // All owner changes in a workspace serialize around its tenant row.
    tx.query_opt("SELECT 1 FROM scenery.scenery_auth_tenants WHERE id=$1::text::uuid AND deleted_at IS NULL FOR UPDATE",
        &[&tenant]).await.map_err(Fault::internal)?.ok_or_else(|| denied("workspace access is disabled"))?;
    if require_member(tx, user, tenant).await? != "owner" {
        return Err(denied("workspace owner permission is required"));
    }
    Ok(())
}

async fn members(db: &impl GenericClient, tenant: &str) -> AuthResult<Reply> {
    let rows = db.query("SELECT u.id::text,u.primary_email,u.display_name,u.avatar_url,m.role,
        (m.disabled_at IS NOT NULL OR u.disabled_at IS NOT NULL)
        FROM scenery.scenery_auth_organization_memberships m
        JOIN scenery.scenery_auth_users u ON u.id=m.user_id
        WHERE m.tenant_id=$1::text::uuid ORDER BY lower(u.display_name),lower(u.primary_email),m.created_at",
        &[&tenant]).await.map_err(Fault::internal)?;
    let members: Vec<_> = rows.iter().map(|row| json!({
        "user_id":row.get::<_,&str>(0),"email":row.get::<_,&str>(1),"display_name":row.get::<_,&str>(2),
        "avatar_url":row.get::<_,&str>(3),"role":row.get::<_,&str>(4),"disabled":row.get::<_,bool>(5),
    })).collect();
    Ok(ok(&json!({"members":members})))
}

async fn invite(
    browser: &Browser<'_>,
    tx: &Transaction<'_>,
    claims: &Claims,
    tenant: &str,
    input: &Value,
) -> AuthResult<Reply> {
    let tenant = uuid(tenant, "tenant_id is invalid")?;
    let address = text(input, "email");
    let normalized = email(address)?;
    let role = text(input, "role");
    let role = checked_role(if role.is_empty() { "member" } else { role })?;
    require_owner(tx, &claims.user, tenant).await?;
    let mut claims = claims.clone();
    claims.tenant = tenant.into();
    let token = issue_once(
        tx,
        "invite_acceptance",
        &claims,
        address,
        &normalized,
        json!({"role":role}),
        7 * 86400,
    )
    .await?;
    audit(
        tx,
        browser,
        "invite_created",
        &claims,
        &json!({"email":normalized,"role":role}),
    )
    .await?;
    let mut result = json!({"ok":true,"email":address});
    if browser.host.development {
        result["dev_invite_token"] = json!(token);
    }
    Ok(ok(&result))
}

async fn accept(
    browser: &Browser<'_>,
    tx: &Transaction<'_>,
    claims: &Claims,
    token: &str,
) -> AuthResult<Reply> {
    if token.is_empty() {
        return Err(invalid("token is required"));
    }
    let user = user(tx, &claims.user).await?;
    if !user.get::<_, bool>("verified") {
        return Err(precondition("email verification is required"));
    }
    let invite = consume_once(tx, "invite_acceptance", token).await?;
    let tenant: &str = invite.get(1);
    if tenant.is_empty() {
        return Err(invalid("invite token is invalid"));
    }
    if !user
        .get::<_, &str>("normalized_primary_email")
        .eq_ignore_ascii_case(invite.get(2))
    {
        return Err(denied("invite email does not match signed-in user"));
    }
    let inviter: &str = invite.get(0);
    let metadata: Value = serde_json::from_slice(invite.get(3)).unwrap_or(Value::Null);
    let role = if metadata["role"] == "owner" {
        "owner"
    } else {
        "member"
    };
    tx.execute("INSERT INTO scenery.scenery_auth_organization_memberships (id,tenant_id,user_id,role,invited_by_user_id,invited_at)
        VALUES(gen_random_uuid(),$1::text::uuid,$2::text::uuid,$3,NULLIF($4,'')::uuid,now())
        ON CONFLICT (user_id,tenant_id) WHERE disabled_at IS NULL DO NOTHING",
        &[&tenant,&claims.user,&role,&inviter]).await.map_err(Fault::internal)?;
    let mut claims = claims.clone();
    set_tenant(tx, &mut claims, tenant).await?;
    let reason = impersonation_reason(tx, &claims).await?;
    let result = bootstrap(browser, tx, &claims, &reason).await?;
    audit(tx, browser, "invite_accepted", &claims, &json!({})).await?;
    Ok(ok(&result))
}

async fn set_tenant(tx: &Transaction<'_>, claims: &mut Claims, tenant: &str) -> AuthResult<()> {
    let changed = tx.execute("UPDATE scenery.scenery_auth_refresh_sessions SET active_tenant_id=$2::text::uuid,updated_at=now()
        WHERE id=$1::text::uuid AND user_id=$3::text::uuid AND revoked_at IS NULL AND expires_at>now()",
        &[&claims.session,&tenant,&claims.user]).await.map_err(Fault::internal)?;
    if changed != 1 {
        return Err(denied("refresh session is invalid"));
    }
    claims.tenant = tenant.into();
    Ok(())
}

async fn change_member(
    tx: &Transaction<'_>,
    claims: &Claims,
    tenant: &str,
    user: &str,
    role: Option<&str>,
) -> AuthResult<Reply> {
    let tenant = uuid(tenant, "tenant_id is invalid")?;
    let user = uuid(user, "user_id is invalid")?;
    let role = role.map(checked_role).transpose()?;
    require_owner(tx, &claims.user, tenant).await?;
    if require_member(tx, user, tenant).await? == "owner" && role != Some("owner") {
        let owners: i64 = tx.query_one("SELECT count(*) FROM scenery.scenery_auth_organization_memberships m
            JOIN scenery.scenery_auth_users u ON u.id=m.user_id
            WHERE m.tenant_id=$1::text::uuid AND m.role='owner' AND m.disabled_at IS NULL AND u.disabled_at IS NULL",
            &[&tenant]).await.map_err(Fault::internal)?.get(0);
        if owners <= 1 {
            return Err(precondition("workspace must retain at least one owner"));
        }
    }
    if let Some(role) = role {
        tx.execute(
            "UPDATE scenery.scenery_auth_organization_memberships SET role=$3,updated_at=now()
            WHERE tenant_id=$1::text::uuid AND user_id=$2::text::uuid AND disabled_at IS NULL",
            &[&tenant, &user, &role],
        )
        .await
        .map_err(Fault::internal)?;
    } else {
        tx.execute("UPDATE scenery.scenery_auth_organization_memberships SET disabled_at=now(),updated_at=now()
            WHERE tenant_id=$1::text::uuid AND user_id=$2::text::uuid AND disabled_at IS NULL",
            &[&tenant,&user]).await.map_err(Fault::internal)?;
    }
    members(tx, tenant).await
}

fn checked_role(role: &str) -> AuthResult<&str> {
    if matches!(role, "owner" | "member") {
        Ok(role)
    } else {
        Err(invalid("role must be owner or member"))
    }
}

async fn start_impersonation(
    browser: &Browser<'_>,
    tx: &Transaction<'_>,
    claims: &Claims,
    input: &Value,
) -> AuthResult<Reply> {
    if !claims.actor.is_empty() {
        return Err(denied("nested impersonation is not allowed"));
    }
    let reason = text(input, "reason");
    if reason.is_empty() {
        return Err(invalid("reason is required"));
    }
    let target = uuid(text(input, "target_user_id"), "target_user_id is invalid")?;
    let tenant = uuid(text(input, "tenant_id"), "tenant_id is invalid")?;
    if claims.user.eq_ignore_ascii_case(target) {
        return Err(invalid("cannot impersonate the current user"));
    }
    let actor = user(tx, &claims.user).await?;
    if actor.get::<_, bool>("disabled") || !actor.get::<_, bool>("can_impersonate_users") {
        return Err(denied("platform impersonation permission is required"));
    }
    if user(tx, target).await?.get::<_, bool>("disabled") {
        return Err(denied("target user is disabled"));
    }
    require_member(tx, target, tenant).await?;
    let id = tx
        .query_one("SELECT gen_random_uuid()::text", &[])
        .await
        .map_err(Fault::internal)?
        .get(0);
    let mut next = Claims {
        user: target.into(),
        tenant: tenant.into(),
        actor: claims.user.clone(),
        impersonation_id: id,
        ..Claims::default()
    };
    let reply = create_session(browser, tx, &mut next, reason, 1800).await?;
    audit(
        tx,
        browser,
        "impersonation_started",
        &next,
        &json!({"reason":reason}),
    )
    .await?;
    Ok(reply)
}

async fn stop_impersonation(
    browser: &Browser<'_>,
    tx: &Transaction<'_>,
    claims: &Claims,
) -> AuthResult<Reply> {
    if claims.actor.is_empty() {
        return Err(precondition("no impersonation session is active"));
    }
    revoke(tx, &claims.session, "impersonation_stopped").await?;
    let tenant = active_tenant(tx, &claims.actor, "").await?;
    let mut next = Claims {
        user: claims.actor.clone(),
        tenant,
        ..Claims::default()
    };
    let reply = create_session(browser, tx, &mut next, "", SESSION_TTL).await?;
    audit(tx, browser, "impersonation_stopped", &next, &json!({})).await?;
    Ok(reply)
}
