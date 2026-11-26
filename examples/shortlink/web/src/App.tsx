import { type FormEvent, useCallback, useEffect, useState } from "react";
import {
  type ClickEvent,
  type LinkItem,
  PublicApiClient,
  type StatsSummary,
  url,
} from "../generated/index";

const baseUrl = url(`${window.location.origin}/api`);
const publicClient = new PublicApiClient({ baseUrl });

const DEMO_EMAIL = "demo@shortlink.local";
const DEMO_PASSWORD = "shortlink-demo-pass";

function extractToken(payload: unknown): string | null {
  if (!payload || typeof payload !== "object") return null;
  const obj = payload as Record<string, unknown>;
  if (typeof obj.token === "string") return obj.token;
  if (typeof obj.access_token === "string") return obj.access_token;
  return null;
}

async function getSessionClient(): Promise<PublicApiClient> {
  const authed = (token: string) =>
    new PublicApiClient({
      baseUrl,
      authentication: {
        credentials: "same-origin",
        authorization: `Bearer ${token}`,
      },
    });

  // 1. Try existing refresh cookie.
  const refreshed = await fetch("/api/auth/refresh", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
  if (refreshed.ok) {
    const token = extractToken(await refreshed.json());
    if (token) return authed(token);
  }

  // 2. Try signing in as the local demo account.
  const login = await fetch("/api/auth/login/email", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: DEMO_EMAIL, password: DEMO_PASSWORD }),
  });
  if (login.ok) {
    const token = extractToken(await login.json());
    if (token) return authed(token);
  }

  // 3. First run: register and confirm via dev verification token.
  const signup = await fetch("/api/auth/signup/email", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      email: DEMO_EMAIL,
      password: DEMO_PASSWORD,
      display_name: "Demo User",
    }),
  });
  if (!signup.ok) {
    throw new Error("Could not sign in or register the local demo account.");
  }
  const signupBody = (await signup.json()) as { dev_verification_token?: unknown };
  if (typeof signupBody.dev_verification_token !== "string") {
    throw new Error("Development email verification token was not returned.");
  }
  const verify = await fetch("/api/auth/email-verification/confirm", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token: signupBody.dev_verification_token }),
  });
  if (!verify.ok) {
    throw new Error("Email verification failed.");
  }
  const token = extractToken(await verify.json());
  if (!token) {
    throw new Error("Session response did not contain a bearer token.");
  }
  return authed(token);
}

function QrBadge({ slug }: { slug: string }) {
  const [src, setSrc] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    let objectUrl: string | null = null;
    publicClient
      .qrSvg({ slug })
      .then((reply) => {
        if (!active || reply.kind !== "result") return;
        const blob = new Blob([new Uint8Array(reply.value)], {
          type: "image/svg+xml",
        });
        objectUrl = URL.createObjectURL(blob);
        setSrc(objectUrl);
      })
      .catch(() => undefined);
    return () => {
      active = false;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [slug]);

  if (!src) return null;
  return <img className="qr-img" src={src} alt={`QR code for ${slug}`} />;
}

export default function App() {
  const [links, setLinks] = useState<readonly LinkItem[]>([]);
  const [stats, setStats] = useState<StatsSummary | null>(null);
  const [clicks, setClicks] = useState<readonly ClickEvent[]>([]);
  const [targetUrl, setTargetUrl] = useState("https://example.com/docs/getting-started");
  const [customSlug, setCustomSlug] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [copiedSlug, setCopiedSlug] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    const [linksReply, statsReply] = await Promise.all([
      publicClient.listLinks({}),
      publicClient.getStats({}),
    ]);
    if (linksReply.kind === "result") {
      setLinks(linksReply.value.items);
    }
    if (statsReply.kind === "result") {
      setStats(statsReply.value);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    const controller = new AbortController();
    async function subscribe() {
      try {
        const reply = await publicClient.liveClicks({}, { signal: controller.signal });
        if (reply.kind !== "result") return;
        for await (const event of reply.events) {
          if (controller.signal.aborted) break;
          setClicks((prev) => {
            if (prev.some((item) => item.clickId === event.data.clickId)) return prev;
            return [event.data, ...prev].slice(0, 25);
          });
          void refresh();
        }
      } catch {
        // Ignore stream cancellation when unmounting.
      }
    }
    void subscribe();
    return () => controller.abort();
  }, [refresh]);

  async function handleCreate(event: FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      const authed = await getSessionClient();
      const slug = customSlug.trim();
      const reply = await authed.createLink({
        targetUrl: targetUrl.trim(),
        ...(slug ? { customSlug: slug } : {}),
      });
      if (reply.kind !== "result") {
        setError(reply.problem.message);
        return;
      }
      setCustomSlug("");
      await refresh();
      // Poll briefly for background metadata enrichment from the TypeScript durable worker.
      setTimeout(() => void refresh(), 750);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create short link.");
    } finally {
      setSubmitting(false);
    }
  }

  async function handleDelete(slug: string) {
    setError("");
    try {
      const authed = await getSessionClient();
      const reply = await authed.deleteLink({ slug });
      if (reply.kind !== "result") {
        setError(reply.problem.message);
        return;
      }
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete link.");
    }
  }

  function handleCopy(slug: string) {
    const shortHref = `${window.location.origin}/api/r/${slug}`;
    void navigator.clipboard?.writeText(shortHref);
    setCopiedSlug(slug);
    setTimeout(() => setCopiedSlug((cur) => (cur === slug ? null : cur)), 1500);
  }

  return (
    <main className="page">
      <header className="header">
        <div>
          <h1>Shortlink</h1>
          <p className="subtitle">
            Polyglot URL shortener — Go, Rust, TypeScript, and Python behind one host
          </p>
        </div>
        <div className="badges">
          <span className="badge">Go · Links &amp; PG</span>
          <span className="badge">Rust · Guard &amp; QR</span>
          <span className="badge">TS · Durable &amp; SSE</span>
          <span className="badge">Python · Stats &amp; Schedule</span>
        </div>
      </header>

      <section className="stats" aria-label="Analytics summary">
        <div className="stat-card">
          <div className="stat-label">Total Links</div>
          <div className="stat-value">{stats ? stats.totalLinks.toString() : "—"}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Total Clicks</div>
          <div className="stat-value">{stats ? stats.totalClicks.toString() : "—"}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Avg Clicks / Link</div>
          <div className="stat-value">{stats ? stats.avgClicksPerLink : "—"}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Top Link</div>
          <div className="stat-value">{stats?.topSlug ?? "—"}</div>
          {stats?.lastRollupAt && (
            <div className="stat-sub">Rollup: {new Date(stats.lastRollupAt).toLocaleTimeString()}</div>
          )}
        </div>
      </section>

      <section className="card">
        <form className="form" onSubmit={handleCreate}>
          <input
            type="url"
            required
            placeholder="https://example.com/some/long/path"
            value={targetUrl}
            onChange={(e) => setTargetUrl(e.target.value)}
            aria-label="Target URL"
          />
          <input
            type="text"
            placeholder="Custom slug (optional)"
            value={customSlug}
            onChange={(e) => setCustomSlug(e.target.value)}
            aria-label="Custom slug"
          />
          <button type="submit" disabled={submitting}>
            {submitting ? "Shortening..." : "Shorten"}
          </button>
        </form>
        {error && <p className="error" role="alert">{error}</p>}
      </section>

      <div className="grid">
        <section className="card">
          <h2 className="section-title">
            <span>Links</span>
            <button type="button" className="ghost" onClick={() => void refresh()}>
              Refresh
            </button>
          </h2>
          {links.length === 0 ? (
            <p className="empty">No links yet. Create one above to see every worker in action.</p>
          ) : (
            <div className="link-list">
              {links.map((item) => {
                const href = `/api/r/${item.slug}`;
                return (
                  <article className="link-row" key={item.slug}>
                    <div className="link-info">
                      <a
                        className="short-url"
                        href={href}
                        target="_blank"
                        rel="noreferrer"
                        onClick={() => setTimeout(() => void refresh(), 400)}
                      >
                        {href}
                      </a>
                      <div className="link-title">
                        {item.title ?? "Enriching metadata..."}
                      </div>
                      <div className="link-target">{item.targetUrl}</div>
                      <div className="link-meta">
                        <span>{item.clicks.toString()} clicks</span>
                        <span>{item.host}</span>
                        {item.enrichedAt && <span>enriched</span>}
                      </div>
                    </div>
                    <div className="link-actions">
                      <QrBadge slug={item.slug} />
                      <div>
                        <button
                          type="button"
                          className="ghost"
                          onClick={() => handleCopy(item.slug)}
                        >
                          {copiedSlug === item.slug ? "Copied" : "Copy"}
                        </button>{" "}
                        <button
                          type="button"
                          className="ghost danger"
                          onClick={() => void handleDelete(item.slug)}
                        >
                          Delete
                        </button>
                      </div>
                    </div>
                  </article>
                );
              })}
            </div>
          )}
        </section>

        <section className="card">
          <h2 className="section-title">
            <span>
              <span className="dot" />
              Live Clicks (SSE)
            </span>
          </h2>
          {clicks.length === 0 ? (
            <p className="empty">Click any short link to watch events stream in real time.</p>
          ) : (
            <div className="feed">
              {clicks.map((ev) => (
                <div className="feed-item" key={ev.clickId}>
                  <span className="feed-slug">/r/{ev.slug}</span> → {ev.targetUrl}
                  <div className="feed-time">
                    {new Date(ev.clickedAt).toLocaleTimeString()} · {ev.clickId}
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </main>
  );
}
