"use client";
import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { ApiError, api, classifyFailure, failureMessage } from "@/lib/api";
import { type Ceremony, createPasskey, passkeyFailure, passkeysSupported, UNSUPPORTED_MESSAGE } from "@/lib/passkeys";
import { useHashToken } from "@/lib/useHashToken";

type Invitation = Ceremony<PublicKeyCredentialCreationOptionsJSON> & { email: string; name: string };

// The server keeps a ceremony for five minutes. An older one is replaced before the passkey
// is made, so a slow reader is not left holding a passkey the server will refuse.
const FRESH_FOR_MS = 3 * 60_000;

const unchanging = () => () => {};

const general = (cause: unknown, fallback: string) =>
  ["offline", "rateLimited", "unconfigured"].includes(classifyFailure(cause)) ? failureMessage(cause, fallback) : fallback;

const explainInvitation = (cause: unknown) => cause instanceof ApiError && cause.status === 401
  ? "This invitation is unavailable, already used, or expired. Ask a DAD administrator for a new one."
  : general(cause, "Your invitation could not be checked right now. Reload this page to try again.");

function explainCreation(cause: unknown): string {
  switch (passkeyFailure(cause)) {
    case "cancelled": return "Passkey creation was cancelled or timed out. Choose “Create a passkey” to try again.";
    case "exists": return "This device already has a passkey for your staff account. Sign in with it, or ask an administrator for a recovery invitation.";
    case "unsupported": return "This device can’t create a suitable passkey. Try another browser, your phone, or a security key.";
    case "refused": return "Your browser refused to create the passkey. Open the invitation link directly in your browser, then try again.";
  }
  return cause instanceof ApiError && cause.status === 401
    ? "The passkey wasn’t accepted. Try again: an invitation allows five attempts and expires 24 hours after it was sent."
    : general(cause, "Activation could not be completed right now. Please try again.");
}

export default function Activate() {
  const { token, clear } = useHashToken("dad.staff.invitation");
  // The server cannot know the browser, so it renders as supported and the client corrects it.
  const supported = useSyncExternalStore(unchanging, passkeysSupported, () => true);
  const [invitation, setInvitation] = useState<{ value: Invitation; at: number } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    const loaded = { value: await api<Invitation>("/donor/staff/activate/options", { method: "POST", body: JSON.stringify({ token }) }), at: Date.now() };
    setInvitation(loaded);
    return loaded;
  }, [token]);
  useEffect(() => {
    if (!token) return;
    let current = true;
    load().catch((cause) => { if (current) setError(explainInvitation(cause)); });
    return () => { current = false; };
  }, [token, load]);
  async function create() {
    if (busy || !invitation) return;
    setBusy(true); setError("");
    let answered = false;
    try {
      const { value } = Date.now() - invitation.at < FRESH_FOR_MS ? invitation : await load();
      const credential = await createPasskey(value.options);
      answered = true;
      await api("/donor/staff/activate", { method: "POST", body: JSON.stringify({ ceremony: value.ceremony, credential }) });
      clear();
      window.location.assign("/staff");
    } catch (cause) {
      setError(explainCreation(cause));
      setBusy(false);
      // An answered ceremony is spent, so another try needs a new one.
      if (answered) load().catch((reload) => setError(explainInvitation(reload)));
    }
  }
  return <Page eyebrow="Staff" title="Activate your staff account">
    {token === "" ? <p className="callout">Open this page from the private invitation link a DAD administrator sent you. If the link has expired, ask them for a replacement invitation.</p>
      : invitation && <>
        <p>This private invitation is for <strong>{invitation.value.name}</strong> ({invitation.value.email}). If that isn’t you, close this page and tell a DAD administrator.</p>
        <p>Create a passkey to finish setting up your account. Your device or password manager saves it, then asks for your fingerprint, face, screen lock, or security key each time you sign in. There is no password to remember.</p>
        {supported
          ? <button type="button" className="button" disabled={busy} onClick={create}>{busy ? "Waiting for your passkey…" : "Create a passkey"}</button>
          : <p className="callout">{UNSUPPORTED_MESSAGE}</p>}
      </>}
    <Announcer status={busy ? "Waiting for your passkey…" : token && !invitation && !error ? "Checking your invitation…" : ""} alert={error} />
  </Page>;
}
