"use client";

import { useState } from "react";
import { api } from "@/lib/api";

export default function SignInPage() {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await api<{ message: string }>("/donor/login", { method: "POST",
        headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email }) });
      setMessage(result.message);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Could not request a link. Please retry.");
    } finally { setBusy(false); }
  }
  return <div className="authLayout"><form className="panel authPanel" onSubmit={submit}>
    <h1>Your donor account</h1>
    <p>Use the email you entered when giving. We’ll email a single-use link to claim your account or sign in.</p>
    <label className="field">Email<input type="email" autoComplete="email" required maxLength={320}
      value={email} onChange={(event) => setEmail(event.target.value)} /></label>
    <button className="button" disabled={busy}>{busy ? "Requesting…" : "Email me a sign-in link"}</button>
    {message && <p role="status">{message}</p>}{error && <p role="alert">{error}</p>}
  </form></div>;
}
