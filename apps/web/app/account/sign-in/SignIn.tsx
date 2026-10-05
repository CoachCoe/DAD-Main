"use client";

import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { api, failureMessage } from "@/lib/api";

export default function SignIn() {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await api<{ message: string }>("/donor/login", { method: "POST", body: JSON.stringify({ email }) });
      setMessage(result.message);
    } catch (cause) {
      setError(failureMessage(cause, "We couldn’t request a link right now. Please retry in a moment."));
    } finally { setBusy(false); }
  }
  return <div className="authLayout"><form className="panel authPanel" onSubmit={submit}>
    <h1>Your donor account</h1>
    <p>Use the email you entered when giving. We’ll email a single-use link to claim your account or sign in.</p>
    <label className="field">Email<input type="email" autoComplete="email" required maxLength={320}
      value={email} onChange={(event) => setEmail(event.target.value)} /></label>
    <button type="submit" className="button full" disabled={busy}>{busy ? "Requesting…" : "Email me a sign-in link"}</button>
    <Announcer status={message} alert={error} />
  </form></div>;
}
