"use client";

import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { api } from "@/lib/api";
import { useApiResource } from "@/lib/useApiResource";

export default function WalletButtons() {
  const { data: providers, resource, reload } = useApiResource<{ apple: boolean; google: boolean }>("/donor/me/wallet");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [googleURL, setGoogleURL] = useState("");
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
  const unchecked = resource.state !== "ready" && resource.state !== "loading";
  return <>
    {(providers?.apple || providers?.google) && <div className="actions">
      {providers.apple && <button type="button" className="secondary" disabled={busy} onClick={() => add("apple")}>Add to Apple Wallet</button>}
      {providers.google && (googleURL ? <a className="secondary" href={googleURL} rel="noreferrer">Save to Google Wallet</a>
        : <button type="button" className="secondary" disabled={busy} onClick={() => add("google")}>Add to Google Wallet</button>)}
    </div>}
    <Announcer status={busy ? "Preparing your Wallet pass…" : ""}
      alert={error || (unchecked ? "Wallet availability could not be checked. Your on-screen code still works." : "")} />
    {unchecked && <button type="button" className="quiet" onClick={reload}>Check Wallet availability again</button>}
  </>;
}
