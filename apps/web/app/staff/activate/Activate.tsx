"use client";
import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { api, classifyFailure, failureMessage } from "@/lib/api";
import { useHashToken } from "@/lib/useHashToken";

type Setup = { email: string; secret: string; uri: string };

// Connection, rate-limit and configuration problems say so; anything else is about the invitation.
const explain = (cause: unknown, fallback: string) =>
  ["offline", "rateLimited", "unconfigured"].includes(classifyFailure(cause)) ? failureMessage(cause, fallback) : fallback;

export default function Activate() {
  const { token, clear } = useHashToken("dad.staff.invitation");
  const [setup, setSetup] = useState<Setup | null>(null);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  async function provision() {
    setBusy(true); setError("");
    try { setSetup(await api<Setup>("/donor/staff/provision", { method: "POST", body: JSON.stringify({ token }) })); }
    catch (cause) { setError(explain(cause, "This invitation is unavailable or expired. Contact a DAD administrator.")); }
    finally { setBusy(false); }
  }
  async function activate(event: React.FormEvent) {
    event.preventDefault(); if (busy) return;
    setBusy(true); setError("");
    try { await api("/donor/staff/activate", { method: "POST", body: JSON.stringify({ token, password, code }) }); setDone(true); setSetup(null); clear(); }
    catch (cause) { setError(explain(cause, "Activation was not confirmed. Check the authenticator code; invitations allow five attempts and expire after 24 hours.")); }
    finally { setBusy(false); setPassword(""); setCode(""); }
  }
  return <Page eyebrow="Staff" title="Activate your staff account">
    {done ? <p>Wait for a new authenticator code, then <Link href="/staff/sign-in">sign in</Link>.</p>
      : token === "" ? <p className="callout">Open this page from the private invitation link a DAD administrator sent you. If the link has expired, ask them for a replacement invitation.</p>
      : !setup ? <><p>Keep this invitation private. It can activate one individual staff account.</p>
        <button type="button" className="button" disabled={busy || !token} onClick={provision}>{busy ? "Checking…" : "Begin secure setup"}</button></> : <>
        <p>Set up {setup.email} in your authenticator app using this private setup key:</p>
        <p><code>{setup.secret}</code></p><p>Choose time-based codes, six digits, with a 30-second interval.</p>
        <p><a href={setup.uri}>Open in an authenticator app</a></p>
        <form onSubmit={activate}><fieldset disabled={busy}><legend>Confirm staff setup</legend>
          <label>New password<input type="password" autoComplete="new-password" minLength={12} maxLength={128} value={password} onChange={(event) => setPassword(event.target.value)} required /></label>
          <label>Authenticator code<input inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" value={code} onChange={(event) => setCode(event.target.value)} required /></label>
          <button type="submit" className="button">{busy ? "Activating…" : "Activate account"}</button>
        </fieldset></form>
      </>}<Announcer status={done ? "Your account is active." : ""} alert={error} />
  </Page>;
}
