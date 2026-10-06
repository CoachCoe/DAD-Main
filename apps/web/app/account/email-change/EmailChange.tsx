"use client";

import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { api, failureMessage } from "@/lib/api";
import { useHashToken } from "@/lib/useHashToken";

const outcomes: Record<string, string> = {
  COMPLETED: "Your email has changed and all devices have been signed out.",
  NEW: "Current address verified. Check your new address for the second confirmation link. Your account email has not changed yet.",
};

export default function EmailChange() {
  const { token: proof, clear } = useHashToken();
  const [busy, setBusy] = useState(false);
  const [stage, setStage] = useState("");
  const [error, setError] = useState("");
  async function confirm() {
    if (busy) return;
    setBusy(true); setError("");
    try {
      const result = await api<{ stage: string }>("/donor/email-change/confirm", { method: "POST", body: JSON.stringify({ token: proof }) });
      clear(); setStage(result.stage);
    } catch (cause) { setError(failureMessage(cause, "The change was not confirmed. Please retry in a moment.")); }
    finally { setBusy(false); }
  }
  return <Page eyebrow="My account" title="Confirm your email change">
    {stage === "COMPLETED" ? <Link className="button" href="/account/sign-in">Sign in using your new address</Link>
      : stage === "NEW" ? null
      : <><p>Continue only if you requested this email change. Both your current and new addresses must be verified. Finishing the change signs out all devices.</p>
        {proof ? <button type="button" className="button" disabled={busy} onClick={confirm}>{busy ? "Confirming…" : "Confirm this address"}</button>
          : proof === "" && <p>Open the confirmation link from your email.</p>}
        <p><Link href="/account/profile">Request a new change from your profile</Link></p></>}
    <Announcer status={outcomes[stage] ?? ""} alert={error} />
  </Page>;
}
