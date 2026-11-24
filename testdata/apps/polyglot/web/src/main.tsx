import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { PublicApiClient, url } from "../generated/index";
import "./style.css";

const api = new PublicApiClient({ baseUrl: url(window.location.origin) });
const amount = 9007199254740993n;

function Pricing() {
  const [status, setStatus] = useState("");
  const [pending, setPending] = useState(false);

  async function loadQuote() {
    setPending(true);
    setStatus("");
    try {
      const [direct, delegated] = await Promise.all([
        api.quote({ amount, note: null }),
        api.delegate({ amount }),
      ]);
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
