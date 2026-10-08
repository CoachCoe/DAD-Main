"use client";

import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { api, failureMessage, type RequestBody, type ResponseBody } from "@/lib/api";

/** The pages an emailed link may return to; the API accepts only these. */
export type SignInReturn = NonNullable<RequestBody<"/donor/login", "post">["next"]>;

/** Requests an emailed single-use link; the response never reveals whether an account exists. */
export function SignInForm({ next }: { next?: SignInReturn }) {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await api<ResponseBody<"/donor/login", "post">>("/donor/login", { method: "POST",
        body: JSON.stringify((next ? { email, next } : { email }) satisfies RequestBody<"/donor/login", "post">) });
      setMessage(result.message);
    } catch (cause) {
      setError(failureMessage(cause, "We couldn’t request a link right now. Please retry in a moment."));
    } finally { setBusy(false); }
  }
  return <form onSubmit={submit}>
    <label>Email<input type="email" autoComplete="email" required maxLength={320}
      value={email} onChange={(event) => setEmail(event.target.value)} /></label>
    <button type="submit" className="button full" disabled={busy}>{busy ? "Requesting…" : "Email me a sign-in link"}</button>
    <Announcer status={message} alert={error} />
  </form>;
}
