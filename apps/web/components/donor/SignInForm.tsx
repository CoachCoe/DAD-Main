"use client";

import { useEffect, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { api, failureMessage, type RequestBody, type ResponseBody } from "@/lib/api";
import { useFocusOnSuccess } from "@/lib/useStepFocus";

/** The pages an emailed link may return to; the API accepts only these. */
export type SignInReturn = NonNullable<RequestBody<"/donor/login", "post">["next"]>;

const RESEND_SECONDS = 30;
// Written by the Thanks page; removed here so the address does not outlive the handoff.
const EMAIL_KEY = "dad.signin.email";

/** Requests an emailed single-use link; the response never reveals whether an account exists. */
export function SignInForm({ next }: { next?: SignInReturn }) {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [sent, setSent] = useState(false);
  const [remaining, setRemaining] = useState(0);
  const heading = useFocusOnSuccess(sent);

  useEffect(() => {
    try {
      const handedOff = sessionStorage.getItem(EMAIL_KEY);
      sessionStorage.removeItem(EMAIL_KEY);
      if (handedOff) setEmail(handedOff);
    } catch {
      // Storage can be blocked; the donor types the address.
    }
  }, []);

  // The per-second text is rendered outside the live region; only the start and the end are announced.
  useEffect(() => {
    if (remaining <= 0) return;
    const timer = setTimeout(() => {
      setRemaining(remaining - 1);
      if (remaining === 1) setMessage("You can resend now.");
    }, 1000);
    return () => clearTimeout(timer);
  }, [remaining]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    // aria-disabled keeps focus on the button, but does not stop the click.
    if (busy || remaining > 0) return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const result = await api<ResponseBody<"/donor/login", "post">>("/donor/login", {
        method: "POST",
        body: JSON.stringify((next ? { email, next } : { email }) satisfies RequestBody<"/donor/login", "post">),
      });
      setMessage(`${result.message} You can resend in ${RESEND_SECONDS} seconds.`);
      setSent(true);
      setRemaining(RESEND_SECONDS);
    } catch (cause) {
      setError(failureMessage(cause, "We couldn’t request a link right now. Please retry in a moment."));
    } finally {
      setBusy(false);
    }
  }
  return (
    <form onSubmit={submit}>
      {sent && (
        <h2 ref={heading} tabIndex={-1}>
          Check your inbox
        </h2>
      )}
      <label>
        Email
        <input
          type="email"
          autoComplete="email"
          required
          maxLength={320}
          value={email}
          onChange={(event) => setEmail(event.target.value)}
        />
      </label>
      <button type="submit" className="button full" aria-disabled={busy || remaining > 0}>
        {busy ? "Requesting…" : sent ? "Resend the link" : "Email me a sign-in link"}
      </button>
      {remaining > 0 && <p className="note">You can resend in {remaining} s.</p>}
      <Announcer status={message} alert={error} />
    </form>
  );
}
