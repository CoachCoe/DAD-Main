"use client";

import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { ApiError, api, failureMessage } from "@/lib/api";
import {
  type Ceremony,
  choosePasskey,
  NO_SUITABLE_PASSKEY_MESSAGE,
  passkeyFailure,
  usePasskeysSupported,
  WAITING_MESSAGE,
} from "@/lib/passkeys";

function explain(cause: unknown): string {
  switch (passkeyFailure(cause)) {
    case "cancelled":
      return "Passkey sign-in was cancelled or timed out. Try again, or use an email link.";
    case "unsupported":
      return NO_SUITABLE_PASSKEY_MESSAGE;
    case "refused":
      return "Your browser refused the passkey request. Try again, or use an email link.";
  }
  if (cause instanceof ApiError && cause.status === 401) {
    return "That passkey isn’t linked to a donor account. Sign in with an email link, then add a passkey from your profile.";
  }
  return failureMessage(cause, "Passkey sign-in could not be completed right now. Please retry, or use an email link.");
}

/** Signs in with a passkey the donor added on their profile; email links remain the way in for everyone else. */
export function PasskeySignIn() {
  // Email sign-in always works, so a browser without passkeys simply isn't offered this.
  const supported = usePasskeysSupported(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (!supported) return null;
  async function signIn() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const { ceremony, options } = await api<Ceremony<PublicKeyCredentialRequestOptionsJSON>>(
        "/donor/passkeys/sign-in/options",
        { method: "POST" },
      );
      const credential = await choosePasskey(options);
      await api("/donor/passkeys/sign-in", { method: "POST", body: JSON.stringify({ ceremony, credential }) });
      window.location.assign("/account/dashboard");
    } catch (cause) {
      setError(explain(cause));
      setBusy(false);
    }
  }
  return (
    <div>
      {/* aria-disabled rather than disabled, so a cancelled prompt leaves focus on the button. */}
      <button type="button" className="secondary full" aria-disabled={busy || undefined} onClick={signIn}>
        Sign in with a passkey
      </button>
      <Announcer status={busy ? WAITING_MESSAGE : ""} alert={error} />
    </div>
  );
}
