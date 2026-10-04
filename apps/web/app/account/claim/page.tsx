"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";

export default function ClaimPage() {
  const token = useRef("");
  const [ready, setReady] = useState(false);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    const secret = new URLSearchParams(window.location.hash.slice(1)).get("token");
    if (secret) token.current = secret;
    window.history.replaceState(null, "", window.location.pathname);
    setReady(Boolean(token.current));
  }, []);

  async function claim() {
    setBusy(true);
    setError("");
    try {
      await api("/donor/claim", { method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token: token.current }) });
      token.current = "";
      setDone(true);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Could not sign in. Please retry.");
    } finally { setBusy(false); }
  }

  return <div className="authLayout"><section className="panel authPanel">
    <h1>{done ? "You’re signed in" : "Claim your donor account"}</h1>
    {done ? <><p><Link href="/account/dashboard">View my dashboard</Link></p>
      <p><Link href="/account/payment-method">Update payment method</Link></p></> : <>
      <p>Confirm to sign in to the donor account associated with this email link.</p>
      {ready ? <button className="button" disabled={busy} onClick={claim}>
        {busy ? "Signing in…" : "Confirm and sign in"}</button>
        : <p>This page needs the link from your email.</p>}
      {error && <p role="alert">{error}</p>}
      <p><Link href="/account/sign-in">Request a new link</Link></p>
    </>}
  </section></div>;
}
