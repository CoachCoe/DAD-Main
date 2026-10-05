"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api";

export default function WalletButtons() {
  const [providers, setProviders] = useState({ apple: false, google: false });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const [googleURL, setGoogleURL] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    api<typeof providers>("/donor/me/wallet", { signal: controller.signal, cache: retry > 0 ? "reload" : "default" }).then((value) => {
      if (!controller.signal.aborted) { setProviders(value); setError(""); }
    }).catch(() => { if (!controller.signal.aborted) setError("Wallet availability could not be checked. Your on-screen code still works."); });
    return () => controller.abort();
  }, [retry]);
  async function add(provider: "apple" | "google") {
    if (busy) return;
    setBusy(true); setError("");
    try {
      if (provider === "google") {
        const result = await api<{ url: string }>("/donor/me/wallet/google", { method: "POST" });
        const url = new URL(result.url);
        if (url.origin !== "https://pay.google.com" || !url.pathname.startsWith("/gp/v/save/")) throw new Error("Invalid Wallet link");
        setGoogleURL(result.url);
      } else {
        const response = await fetch("/api/donor/me/wallet/apple", { method: "POST", credentials: "include" });
        if (!response.ok || response.headers.get("content-type") !== "application/vnd.apple.pkpass") throw new Error("Pass unavailable");
        const url = URL.createObjectURL(await response.blob());
        const link = document.createElement("a");
        link.href = url; link.download = "dad-referral.pkpass"; document.body.append(link); link.click(); link.remove();
        setTimeout(() => URL.revokeObjectURL(url), 60_000);
      }
    } catch { setError("The Wallet pass could not be prepared. Please retry. Your on-screen code still works."); }
    finally { setBusy(false); }
  }
  return <div>
    {providers.apple && <button type="button" disabled={busy} onClick={() => add("apple")}>Add to Apple Wallet</button>}
    {providers.google && (googleURL ? <a href={googleURL} rel="noreferrer">Save to Google Wallet</a>
      : <button type="button" disabled={busy} onClick={() => add("google")}>Add to Google Wallet</button>)}
    {busy && <p role="status">Preparing your Wallet pass…</p>}
    {error && <><p role="alert">{error}</p><button type="button" onClick={() => setRetry((value) => value + 1)}>Check Wallet availability again</button></>}
  </div>;
}
