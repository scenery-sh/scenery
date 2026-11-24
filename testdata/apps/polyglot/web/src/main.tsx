import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { PublicApiClient, url } from "../generated/index";
import "./style.css";

const baseUrl = url(window.location.origin);
const api = new PublicApiClient({ baseUrl });
const amount = 9007199254740993n;

async function sessionClient(): Promise<PublicApiClient | undefined> {
  const response = await fetch("/auth/refresh", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
  if (response.status === 401 || response.status === 404) return undefined;
  if (!response.ok) throw new Error("Session refresh failed");
  const session: { token?: unknown } = await response.json();
  if (typeof session.token !== "string") throw new Error("Session response is invalid");
  return new PublicApiClient({ baseUrl, authentication: { credentials: "same-origin", authorization: `Bearer ${session.token}` } });
}

function Pricing() {
  const [status, setStatus] = useState("");
  const [pending, setPending] = useState(false);

  async function loadQuote() {
    setPending(true);
    setStatus("");
    try {
      const session = await sessionClient();
      const [direct, delegated] = await Promise.all(session
        ? [session.quoteViaQuoteSessionHttp({ amount, note: null }), session.delegateViaDelegateSessionHttp({ amount })]
        : [api.quoteViaQuoteHttp({ amount, note: null }), api.delegateViaDelegateHttp({ amount })]);
      if (direct.kind !== "result" || delegated.kind !== "result") {
        setStatus("The quote could not be loaded.");
        return;
      }
      setStatus(`Quote ${direct.value.amount} from ${direct.value.vendor}. Delegated quote ${delegated.value.amount}.`);
    } catch {
      setStatus("The quote could not be loaded.");
    } finally {
      setPending(false);
    }
  }

  return (
    <main>
      <h1>Pricing</h1>
      <button type="button" onClick={loadQuote} disabled={pending}>Get quote</button>
      <p role="status">{status}</p>
    </main>
  );
}

const root = document.getElementById("app");
if (root) createRoot(root).render(<Pricing />);
