"use client";
import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { ApiError, api, classifyFailure, failureMessage } from "@/lib/api";
import { type Ceremony, choosePasskey, NO_SUITABLE_PASSKEY_MESSAGE, passkeyFailure, usePasskeysSupported, UNSUPPORTED_MESSAGE, WAITING_MESSAGE } from "@/lib/passkeys";


function explain(cause: unknown): string {
  switch (passkeyFailure(cause)) {
    case "cancelled": return "Sign-in was cancelled or timed out. Choose “Sign in with a passkey” to try again.";
    case "unsupported": return NO_SUITABLE_PASSKEY_MESSAGE;
    case "refused": return "Your browser refused the passkey request. Make sure you opened the staff sign-in page directly, then try again.";
  }
  if (cause instanceof ApiError && cause.status === 401) {
    return "That passkey wasn’t accepted. Use the passkey you created for DAD staff, or ask an administrator for a recovery invitation.";
  }
  return ["offline", "rateLimited", "unconfigured"].includes(classifyFailure(cause))
    ? failureMessage(cause, "") : "Sign-in could not be completed right now. Please try again.";
}

export default function SignIn() {
  const supported = usePasskeysSupported(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function signIn() {
    if (busy) return;
    setBusy(true); setError("");
    try {
      const { ceremony, options } = await api<Ceremony<PublicKeyCredentialRequestOptionsJSON>>("/donor/staff/sign-in/options", { method: "POST" });
      const credential = await choosePasskey(options);
      await api("/donor/staff/sign-in", { method: "POST", body: JSON.stringify({ ceremony, credential }) });
      window.location.assign("/staff");
    } catch (cause) { setError(explain(cause)); setBusy(false); }
  }
  return <Page eyebrow="Staff" title="DAD staff sign-in">
    <p>Sign in with the passkey you created when you activated your staff account. Your device or password manager will ask you to confirm it’s you with your fingerprint, face, screen lock, or security key.</p>
    {supported
      // aria-disabled rather than disabled, so a cancelled prompt leaves focus on the button.
      ? <button type="button" className="button" aria-disabled={busy || undefined} onClick={signIn}>Sign in with a passkey</button>
      : <p className="callout">{UNSUPPORTED_MESSAGE}</p>}
    <Announcer status={busy ? WAITING_MESSAGE : ""} alert={error} />
    <p>Lost your passkey? Ask a DAD administrator for a recovery invitation. Donor accounts do not grant staff access.</p>
  </Page>;
}
