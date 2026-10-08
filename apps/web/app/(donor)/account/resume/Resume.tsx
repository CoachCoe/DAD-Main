"use client";

import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { SignInPrompt } from "@/components/donor/SignInPrompt";
import { api, ApiError, unconfirmedMessage } from "@/lib/api";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { useStepFocus } from "@/lib/useStepFocus";
import { formatDate } from "@/lib/dates";

type Options = { state: "unavailable" } | { state: "ready" | "saved"; pause_id: string;
  next_renewal: string; request_id?: string; confirmed?: boolean };


export default function Resume() {
  const [signedOut, setSignedOut] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [requestKey, setRequestKey] = useState<string | null>(null);
  const { resource, data, failed, reload, setData } = useApiResource<Options>(signedOut ? null : "/donor/me/recurring/resume", {
    onLoad: (value) => { setConfirming(false); setError(""); setRequestKey(value.state === "saved" ? value.request_id ?? null : null); },
  });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const step = useStepFocus(confirming);
  const messages = resourceMessages(resource, { loading: "Checking the next renewal…",
    failure: "We could not confirm your next renewal right now. Please refresh in a moment." });

  async function resume() {
    if (!data || data.state === "unavailable" || busy) return;
    const key = requestKey ?? crypto.randomUUID();
    setRequestKey(key); setBusy(true); setError(""); setMessage("");
    try {
      const result = await api<{ request_id: string; pause_id: string; next_renewal: string; confirmed: boolean }>(
        "/donor/me/recurring/resume", { method: "POST", headers: { "Idempotency-Key": key },
          body: JSON.stringify({ pause_id: data.pause_id, next_renewal: data.next_renewal }) });
      setData({ state: "saved", ...result }); setConfirming(false);
      setMessage(result.confirmed ? `Resumption confirmed for ${formatDate(result.next_renewal)}.` : "Your resumption request is not confirmed yet.");
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError(unconfirmedMessage(cause, "Resumption is not confirmed. Retry the saved request or refresh its status."));
    } finally { setBusy(false); }
  }
  const confirmButton = <button type="button" className="button" disabled={busy} onClick={resume}>{busy ? "Checking resumption…" : "Confirm resume giving"}</button>;
  return <Page eyebrow="My account" title="Resume monthly giving" back={{ href: "/account/pause", label: "My pause" }}>
    {signedOut || resource.state === "signedOut" ? <SignInPrompt>Sign in to manage your giving</SignInPrompt> : <>
      {data?.state === "unavailable" && <p>No pause is currently available to shorten. Giving may already resume at the next renewal.</p>}
      {data && data.state !== "unavailable" && <>
        {data.confirmed ? !message && <p>Resumption confirmed for <time dateTime={data.next_renewal}>{formatDate(data.next_renewal)}</time>.</p> : <>
          <p className="lead">Resume at your next regular renewal: <time dateTime={data.next_renewal}>{formatDate(data.next_renewal)}</time>.</p>
          {data.state === "saved" && !message && <p>Your resumption request is not confirmed yet.</p>}
          <p>This does not charge you immediately or collect renewals skipped during your pause.</p>
          {data.state === "saved" ? confirmButton : confirming ? <div className="callout">
            <h2 ref={step.heading} tabIndex={-1}>Confirm resumption</h2>
            <div className="actions">
              {confirmButton}
              <button type="button" className="secondary" disabled={busy || requestKey !== null} onClick={() => setConfirming(false)}>Back</button>
            </div>
          </div> : <button type="button" ref={step.trigger} className="button" onClick={() => setConfirming(true)}>Resume at next renewal</button>}
        </>}
        <p className="note">If you already skipped a renewal, your streak restarts only after a successful recurring charge.
          If no renewal was skipped, successful giving can continue the existing streak.</p>
      </>}
      <Announcer status={messages.status || message} alert={error || messages.alert} />
      {(failed || error || data?.state === "saved") && <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh resumption status</button>}
    </>}
  </Page>;
}
