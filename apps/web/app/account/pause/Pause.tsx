"use client";

import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, ApiError, unconfirmedMessage } from "@/lib/api";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { useStepFocus } from "@/lib/useStepFocus";

type Dates = { months: number; starts_at: string; resumes_at: string };
type Pause = Dates & { request_id: string; confirmed: boolean };
type Options = { state: string; subscription_id?: string; pause: Pause | null; options: Dates[] };

const date = (value: string) => new Date(value).toLocaleString();
const renewals = (months: number) => `${months} renewal${months === 1 ? "" : "s"}`;

export default function PauseGiving() {
  const [signedOut, setSignedOut] = useState(false);
  const [choice, setChoice] = useState<Dates | null>(null);
  const [opener, setOpener] = useState<number | null>(null);
  const [requestKey, setRequestKey] = useState<string | null>(null);
  const { resource, data, reload, setData } = useApiResource<Options>(signedOut ? null : "/donor/me/recurring/pause", {
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
  const absent = signedOut || resource.state === "signedOut";
  return <section className="panel">
    <h1>Pause monthly giving</h1>
    <p><Link href="/account/recurring">Manage monthly giving</Link></p>
    <p>Sandbox only. These controls affect test subscriptions.</p>
    {absent ? <p><Link href="/account/sign-in">Sign in to manage your giving</Link></p> : <>
      {data?.pause ? <>
        {!message && <p>{data.pause.confirmed ? "Pause confirmed." : "Pause not yet confirmed."}</p>}
        <p>Original pause starts {date(data.pause.starts_at)}.
          Giving automatically resumes {date(data.pause.resumes_at)}.</p>
        {!data.pause.confirmed && <button type="button" className="button" disabled={busy} onClick={() => data.pause && confirm(data.pause)}>Retry saved pause</button>}
      </> : data && data.options.length === 0 ? <p>No monthly gift is currently available to pause.</p> : data && <>
        {!choice ? <div className="actionRow">{data.options.map((option) => <button type="button" className="secondary" key={option.months}
          ref={option.months === opener ? step.trigger : undefined} onClick={() => { setOpener(option.months); setChoice(option); }}>
          Skip {renewals(option.months)}
        </button>)}</div> : <div className="confirmStep">
          <h2 ref={step.heading} tabIndex={-1}>Confirm your pause</h2>
          <p>Skip {renewals(choice.months)}, starting <time dateTime={choice.starts_at}>{date(choice.starts_at)}</time>.</p>
          <p>Giving automatically resumes <time dateTime={choice.resumes_at}>{date(choice.resumes_at)}</time>.</p>
          <button type="button" className="button" disabled={busy} onClick={() => confirm(choice)}>{busy ? "Checking pause…" : "Confirm pause"}</button>
          <button type="button" className="secondary" disabled={busy || requestKey !== null} onClick={() => setChoice(null)}>Back</button>
        </div>}
      </>}
      <p>Your streak ends at the first skipped renewal. Your Founding Donor badge stays yours.
        Pausing does not refund gifts or forgive an outstanding invoice.</p>
      <p><Link href="/account/resume">Resume at next renewal</Link></p>
      <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh pause dates</button>
    </>}
    <Announcer status={absent ? "" : messages.status || message} alert={absent ? "" : error || messages.alert} />
  </section>;
}
