"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";

type Setup = { email: string; secret: string; uri: string };
export default function StaffActivate() {
  const [token, setToken] = useState("");
  const [setup, setSetup] = useState<Setup | null>(null);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    setToken(new URLSearchParams(window.location.hash.slice(1)).get("token") || "");
    window.history.replaceState(null, "", window.location.pathname);
  }, []);
  async function provision() {
    setBusy(true); setError("");
    try { setSetup(await api<Setup>("/donor/staff/provision", { method: "POST", body: JSON.stringify({ token }) })); }
    catch { setError("This invitation is unavailable or expired. Contact a DAD administrator."); }
    finally { setBusy(false); }
  }
  async function activate(event: React.FormEvent) {
    event.preventDefault(); if (busy) return;
    setBusy(true); setError("");
    try { await api("/donor/staff/activate", { method: "POST", body: JSON.stringify({ token, password, code }) }); setDone(true); setSetup(null); setToken(""); }
    catch { setError("Activation was not confirmed. Check the authenticator code; invitations allow five attempts and expire after 24 hours."); }
    finally { setBusy(false); setPassword(""); setCode(""); }
  }
  return <section className="panel"><h1>Activate your staff account</h1>
    {done ? <p role="status">Your account is active. Wait for a new authenticator code, then <Link href="/staff/sign-in">sign in</Link>.</p> : <>
      {!setup ? <><p>Keep this invitation private. It can activate one individual staff account.</p>
        <button disabled={busy || !token} onClick={provision}>{busy ? "Checking…" : "Begin secure setup"}</button></> : <>
        <p>Set up {setup.email} in your authenticator app using this private setup key:</p>
        <p><code>{setup.secret}</code></p><p>Choose time-based codes, six digits, with a 30-second interval.</p>
        <p><a href={setup.uri}>Open in an authenticator app</a></p>
        <form onSubmit={activate}><fieldset disabled={busy}><legend>Confirm staff setup</legend>
          <label>New password<input type="password" autoComplete="new-password" minLength={12} maxLength={128} value={password} onChange={(event) => setPassword(event.target.value)} required /></label>
          <label>Authenticator code<input inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" value={code} onChange={(event) => setCode(event.target.value)} required /></label>
          <button type="submit">{busy ? "Activating…" : "Activate account"}</button>
        </fieldset></form>
      </>}
    </>}{error && <p role="alert">{error}</p>}
  </section>;
}
