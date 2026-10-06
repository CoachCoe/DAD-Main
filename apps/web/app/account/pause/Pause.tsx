"use client";

import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { StaleNotice } from "@/components/donor/StaleNotice";
import { SignInPrompt } from "@/components/donor/SignInPrompt";
import { api, ApiError, unconfirmedMessage } from "@/lib/api";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { useStepFocus } from "@/lib/useStepFocus";
import { formatDateTime } from "@/lib/dates";

type Dates = { months: number; starts_at: string; resumes_at: string };
type Pause = Dates & { request_id: string; confirmed: boolean };
type Options = { state: string; stale?: boolean; subscription_id?: string; pause: Pause | null; options: Dates[] };

const renewals = (months: number) => `${months} renewal${months === 1 ? "" : "s"}`;

export default function PauseGiving() {
  const [signedOut, setSignedOut] = useState(false);
  const [choice, setChoice] = useState<Dates | null>(null);
  const [opener, setOpener] = useState<number | null>(null);
  const [requestKey, setRequestKey] = useState<string | null>(null);
  const { resource, data, failed, reload, setData } = useApiResource<Options>(signedOut ? null : "/donor/me/recurring/pause", {
    onLoad: (value) => { setChoice(null); setError(""); setRequestKey(value.pause?.request_id ?? null); },
  });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const step = useStepFocus(choice !== null);
  const messages = resourceMessages(resource, { loading: "Checking pause dates…",
    failure: "We could not confirm your pause dates right now. Please refresh in a moment." });

  async function confirm(dates: Dates) {
    if (!data?.subscription_id || busy) return;
    const key = requestKey ?? crypto.randomUUID();
    setRequestKey(key); setBusy(true); setError(""); setMessage("");
    try {
      const result = await api<{ pause: Pause }>("/donor/me/recurring/pause", {
        method: "POST", headers: { "Idempotency-Key": key },
        body: JSON.stringify({ subscription_id: data.subscription_id, months: dates.months,
          starts_at: dates.starts_at, resumes_at: dates.resumes_at }),
      });
      setData({ ...data, pause: result.pause, options: [] }); setChoice(null);
      setMessage(result.pause.confirmed ? "Pause confirmed." : "Pause not yet confirmed.");
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError(unconfirmedMessage(cause, "Pause is not confirmed. Retry the same request, or refresh to check its status."));
    } finally { setBusy(false); }
  }
  return <Page eyebrow="My account" title="Pause monthly giving" back={{ href: "/account/recurring", label: "Manage monthly giving" }}>
    {signedOut || resource.state === "signedOut" ? <SignInPrompt>Sign in to manage your giving</SignInPrompt> : <>
      {data?.stale && <StaleNotice />}
      {data?.pause ? <div className="callout">
        {!message && <p>{data.pause.confirmed ? "Pause confirmed." : "Pause not yet confirmed."}</p>}
        <p>Original pause starts {formatDateTime(data.pause.starts_at)}. Giving automatically resumes {formatDateTime(data.pause.resumes_at)}.</p>
        {!data.pause.confirmed && <button type="button" className="button" disabled={busy || data.stale} onClick={() => data.pause && confirm(data.pause)}>Retry saved pause</button>}
        <p><Link href="/account/resume">Resume at next renewal</Link></p>
      </div> : data && data.options.length === 0 ? <p>No monthly gift is currently available to pause.</p> : data && <>
        {!choice ? <>
          <p className="lead">Skip up to three monthly renewals. You’ll see the exact resume date before you confirm.</p>
          <div className="actions">{data.options.map((option) => <button type="button" className="secondary" key={option.months} disabled={data.stale}
            ref={option.months === opener ? step.trigger : undefined} onClick={() => { setOpener(option.months); setChoice(option); }}>
            Skip {renewals(option.months)}
          </button>)}</div>
        </> : <div className="callout">
          <h2 ref={step.heading} tabIndex={-1}>Confirm your pause</h2>
          <p>Skip {renewals(choice.months)}, starting <time dateTime={choice.starts_at}>{formatDateTime(choice.starts_at)}</time>.</p>
          <p>Giving automatically resumes <time dateTime={choice.resumes_at}>{formatDateTime(choice.resumes_at)}</time>.</p>
          <div className="actions">
            <button type="button" className="button" disabled={busy || data.stale} onClick={() => confirm(choice)}>{busy ? "Checking pause…" : "Confirm pause"}</button>
            <button type="button" className="secondary" disabled={busy || requestKey !== null} onClick={() => setChoice(null)}>Back</button>
          </div>
        </div>}
      </>}
      <p className="note">Your streak ends at the first skipped renewal. Your Founding Donor badge stays yours.
        Pausing does not refund gifts or forgive an outstanding invoice. Sandbox only: these controls affect test subscriptions.</p>
      <Announcer status={messages.status || message} alert={error || messages.alert} />
      {(failed || error || data?.stale) && <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh pause dates</button>}
    </>}
  </Page>;
}
