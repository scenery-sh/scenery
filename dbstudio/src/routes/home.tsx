import { useMemo, useState } from "react";
import { resolveStudioTarget } from "../lib/studio-url";

export function HomeRoute() {
  const [loaded, setLoaded] = useState(false);
  const target = useMemo(() => resolveStudioTarget(window.location), []);

  return (
    <main className="app-shell">
      {!loaded ? (
        <div className="loading-overlay" role="status" aria-live="polite">
          <div className="loading-card">
            <div className="loading-eyebrow">onlava DB Studio</div>
            <h1>Loading Drizzle Studio</h1>
            <p>
              Proxy target: <code>{target.host}</code>
              <span>:</span>
              <code>{target.port}</code>
            </p>
            <a href={target.href} target="_blank" rel="noreferrer">
              Open directly
            </a>
          </div>
        </div>
      ) : null}
      <iframe
        className="studio-frame"
        src={target.href}
        title="Drizzle Studio"
        onLoad={() => setLoaded(true)}
      />
    </main>
  );
}
