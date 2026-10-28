"use client";

import { useEffect, useRef, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { api, failureMessage, type RequestBody } from "@/lib/api";

export function PhoneSignIn() {
  const [phone, setPhone] = useState("");
  const [code, setCode] = useState("");
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const codeInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (sent) codeInput.current?.focus();
  }, [sent]);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      if (sent) {
        await api("/donor/phone/sign-in", {
          method: "POST",
          body: JSON.stringify({ phone, code } satisfies RequestBody<"/donor/phone/sign-in", "post">),
        });
        window.location.assign("/account/dashboard");
      } else {
        await api("/donor/phone/sign-in/options", {
          method: "POST",
          body: JSON.stringify({ phone } satisfies RequestBody<"/donor/phone/sign-in/options", "post">),
        });
        setSent(true);
      }
    } catch (cause) {
      setError(failureMessage(cause, "Phone sign-in could not be completed. Please retry, or use an email link."));
    } finally {
      setBusy(false);
    }
  }
  return (
    <details>
      <summary>Sign in with your phone</summary>
      <p className="note">Use the US phone number saved on your donor profile. You can always sign in by email.</p>
      <form onSubmit={submit}>
        <label>
          Phone number
          <input
            type="tel"
            autoComplete="tel"
            required
            maxLength={32}
            value={phone}
            readOnly={sent}
            onChange={(event) => setPhone(event.target.value)}
          />
        </label>
        {sent && (
          <label>
            Verification code
            <input
              ref={codeInput}
              type="text"
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9]{6}"
              required
              minLength={6}
              maxLength={6}
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
          </label>
        )}
        <button className="secondary full" type="submit" disabled={busy}>
          {busy ? "Please wait…" : sent ? "Verify and sign in" : "Text me a code"}
        </button>
        {sent && (
          <button
            type="button"
            className="secondary full"
            disabled={busy}
            onClick={() => {
              setSent(false);
              setCode("");
              setError("");
            }}
          >
            Change number or request a new code
          </button>
        )}
        <Announcer
          status={sent ? "If this number is linked to an account, a code is on its way. It expires in 10 minutes." : ""}
          alert={error}
        />
      </form>
    </details>
  );
}
