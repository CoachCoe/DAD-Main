"use client";
import { useState } from "react";
import { api } from "@/lib/api";

export default function StaffSignIn() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(event: React.FormEvent) {
    event.preventDefault(); if (busy) return;
    setBusy(true); setError("");
    try { await api("/donor/staff/sign-in", { method: "POST", body: JSON.stringify({ email, password, code }) }); window.location.assign("/staff"); }
    catch { setError("Sign-in was not confirmed. Check your credentials and use a fresh authenticator code. After five failed attempts, wait 15 minutes."); }
    finally { setBusy(false); setCode(""); setPassword(""); }
  }
  return <section className="panel"><h1>DAD staff sign-in</h1>
    <p>Individual staff access requires a password and an authenticator code. Donor accounts do not grant staff access.</p>
    <form onSubmit={submit}><fieldset disabled={busy}><legend>Staff credentials</legend>
      <label>Email<input type="email" autoComplete="username" value={email} onChange={(event) => setEmail(event.target.value)} required /></label>
      <label>Password<input type="password" autoComplete="current-password" maxLength={128} value={password} onChange={(event) => setPassword(event.target.value)} required /></label>
      <label>Authenticator code<input inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" value={code} onChange={(event) => setCode(event.target.value)} required /></label>
      <button type="submit">{busy ? "Checking…" : "Sign in"}</button>
    </fieldset></form>{error && <p role="alert">{error}</p>}
  </section>;
}
