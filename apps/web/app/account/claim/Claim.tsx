"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, failureMessage } from "@/lib/api";

export default function Claim() {
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
      await api("/donor/claim", { method: "POST", body: JSON.stringify({ token: token.current }) });
      token.current = "";
      setDone(true);
    } catch (cause) {
      setError(failureMessage(cause, "We couldn’t sign you in right now. Please retry in a moment."));
    } finally { setBusy(false); }
  }

  return <div className="authLayout"><section className="panel authPanel">
    <h1>{done ? "You’re signed in" : "Claim your donor account"}</h1>
    {done ? <><p>Your streak, referral link, and referral code are ready.</p>
      <p><Link className="button" href="/account/dashboard">View my dashboard</Link></p>
      <p><Link href="/account/payment-method">Update payment method</Link></p></> : <>
      <p>Confirm to sign in to the donor account associated with this email link.</p>
      {ready ? <button type="button" className="button full" disabled={busy} onClick={claim}>
        {busy ? "Signing in…" : "Confirm and sign in"}</button>
        : <p>This page needs the link from your email.</p>}
      <p><Link href="/account/sign-in">Request a new link</Link></p>
    </>}
    <Announcer status={done ? "You’re signed in." : ""} alert={error} />
  </section></div>;
}
