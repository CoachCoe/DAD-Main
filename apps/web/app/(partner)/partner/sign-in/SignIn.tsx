"use client";

import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { api, failureMessage, type RequestBody, type ResponseBody } from "@/lib/api";

/** Requests an emailed partner sign-in link; the response never reveals whether the address has access. */
export default function SignIn() {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const result = await api<ResponseBody<"/donor/partner/sign-in", "post">>("/donor/partner/sign-in", {
        method: "POST",
        body: JSON.stringify({ email } satisfies RequestBody<"/donor/partner/sign-in", "post">),
      });
      setMessage(result.message);
    } catch (cause) {
      setError(failureMessage(cause, "We couldn’t request a link right now. Please retry in a moment."));
    } finally {
      setBusy(false);
    }
  }
  return (
    <form onSubmit={submit}>
      <label>
        Work email
        <input
          type="email"
          autoComplete="email"
          required
          maxLength={320}
          value={email}
          onChange={(event) => setEmail(event.target.value)}
        />
      </label>
      <button type="submit" className="button full" disabled={busy}>
        {busy ? "Requesting…" : "Email me a sign-in link"}
      </button>
      <Announcer status={message} alert={error} />
    </form>
  );
}
