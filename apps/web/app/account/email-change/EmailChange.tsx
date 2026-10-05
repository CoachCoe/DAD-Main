"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, failureMessage } from "@/lib/api";

const outcomes: Record<string, string> = {
  COMPLETED: "Your email has changed and all devices have been signed out.",
  NEW: "Current address verified. Check your new address for the second confirmation link. Your account email has not changed yet.",
};

export default function EmailChange() {
  const proof = useRef("");
  const [ready, setReady] = useState(false);
  const [busy, setBusy] = useState(false);
  const [stage, setStage] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    const token = new URLSearchParams(window.location.hash.slice(1)).get("token");
    if (token) proof.current = token;
    window.history.replaceState(null, "", window.location.pathname);
    setReady(Boolean(proof.current));
  }, []);
  async function confirm() {
    if (busy) return;
    setBusy(true); setError("");
    try {
      const result = await api<{ stage: string }>("/donor/email-change/confirm", { method: "POST", body: JSON.stringify({ token: proof.current }) });
      proof.current = ""; setStage(result.stage);
    } catch (cause) { setError(failureMessage(cause, "The change was not confirmed. Please retry in a moment.")); }
    finally { setBusy(false); }
  }
  return <section className="panel">
    <h1>Confirm your email change</h1>
    {stage === "COMPLETED" ? <p><Link href="/account/sign-in">Sign in using your new address</Link></p>
      : stage === "NEW" ? null
      : <><p>Continue only if you requested this email change. Both your current and new addresses must be verified. Finishing the change signs out all devices.</p>
        {ready ? <button type="button" className="button" disabled={busy} onClick={confirm}>{busy ? "Confirming…" : "Confirm this address"}</button> : <p>Open the confirmation link from your email.</p>}
        <p><Link href="/account/profile">Request a new change from your profile</Link></p></>}
    <Announcer status={outcomes[stage] ?? ""} alert={error} />
  </section>;
}
