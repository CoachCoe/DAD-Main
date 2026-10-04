"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

type Dates = { months: number; starts_at: string; resumes_at: string };
type Pause = Dates & { request_id: string; confirmed: boolean };
type Options = { state: string; subscription_id?: string; pause: Pause | null; options: Dates[] };

export default function PausePage() {
  const [data, setData] = useState<Options | null>(null);
  const [choice, setChoice] = useState<Dates | null>(null);
  const [error, setError] = useState("");
  const [signedOut, setSignedOut] = useState(false);
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const key = useRef<string | null>(null);
  useEffect(() => {
    let active = true;
    setData(null); setChoice(null); setError("");
    api<Options>("/donor/me/recurring/pause").then((value) => {
      if (active) { setData(value); key.current = value.pause?.request_id ?? null; }
    }).catch((cause) => {
      if (!active) return;
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("We could not confirm your pause dates. Check your connection and refresh.");
    });
    return () => { active = false; };
  }, [retry]);
  async function confirm(dates: Dates) {
    if (!data?.subscription_id || busy) return;
    setBusy(true); setError("");
    key.current ??= crypto.randomUUID();
    try {
      const result = await api<{ pause: Pause }>("/donor/me/recurring/pause", {
        method: "POST", headers: { "Idempotency-Key": key.current },
        body: JSON.stringify({ subscription_id: data.subscription_id, months: dates.months,
          starts_at: dates.starts_at, resumes_at: dates.resumes_at }),
      });
      setData({ ...data, pause: result.pause, options: [] }); setChoice(null);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("Pause is not confirmed. Retry the same request, or refresh to check its status.");
    } finally { setBusy(false); }
  }
  const date = (value: string) => new Date(value).toLocaleString();
  return <section className="panel">
    <h1>Pause monthly giving</h1>
    <p><Link href="/account/recurring">Manage monthly giving</Link></p>
    <p>Sandbox only. These controls affect test subscriptions.</p>
    {signedOut ? <Link href="/account/sign-in">Sign in to manage your giving</Link> : <>
      {!data && !error && <p role="status">Checking pause dates…</p>}
      {data?.pause ? <>
        <p role="status">{data.pause.confirmed ? "Pause confirmed." : "Pause not yet confirmed."}</p>
        <p>Original pause starts {date(data.pause.starts_at)}.
          Giving automatically resumes {date(data.pause.resumes_at)}.</p>
        {!data.pause.confirmed && <button disabled={busy} onClick={() => confirm(data.pause!)}>Retry saved pause</button>}
      </> : data && data.options.length === 0 ? <p>No monthly gift is currently available to pause.</p> : data && <>
        {!choice ? <div>{data.options.map((option) => <button key={option.months} onClick={() => setChoice(option)}>
          Skip {option.months} renewal{option.months === 1 ? "" : "s"}
        </button>)}</div> : <div>
          <h2>Confirm your pause</h2>
          <p>Skip {choice.months} renewal{choice.months === 1 ? "" : "s"}, starting <time dateTime={choice.starts_at}>{date(choice.starts_at)}</time>.</p>
          <p>Giving automatically resumes <time dateTime={choice.resumes_at}>{date(choice.resumes_at)}</time>.</p>
          <button disabled={busy} onClick={() => confirm(choice)}>{busy ? "Checking pause…" : "Confirm pause"}</button>
          <button disabled={busy || key.current !== null} onClick={() => setChoice(null)}>Back</button>
        </div>}
      </>}
      <p>Your streak ends at the first skipped renewal. Your Founding Donor badge stays yours.
        Pausing does not refund gifts or forgive an outstanding invoice.</p>
      <p><Link href="/account/resume">Resume at next renewal</Link></p>
      {error && <p role="alert">{error}</p>}
      <button disabled={busy} onClick={() => setRetry((value) => value + 1)}>Refresh pause dates</button>
    </>}
  </section>;
}
