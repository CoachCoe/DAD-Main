"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

type Options = { state: "unavailable" } | { state: "ready" | "saved"; pause_id: string;
  next_renewal: string; request_id?: string; confirmed?: boolean };

export default function ResumePage() {
  const [data, setData] = useState<Options | null>(null);
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState("");
  const [signedOut, setSignedOut] = useState(false);
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const key = useRef<string | null>(null);
  useEffect(() => {
    let active = true;
    setData(null); setError(""); setConfirm(false);
    api<Options>("/donor/me/recurring/resume", { cache: retry > 0 ? "reload" : "default" }).then((value) => {
      if (active) { setData(value); key.current = value.state === "saved" ? value.request_id ?? null : null; }
    }).catch((cause) => {
      if (!active) return;
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("We could not confirm your next renewal. Check your connection and refresh.");
    });
    return () => { active = false; };
  }, [retry]);
  async function resume() {
    if (!data || data.state === "unavailable" || busy) return;
    setBusy(true); setError("");
    key.current ??= crypto.randomUUID();
    try {
      const result = await api<{ request_id: string; pause_id: string; next_renewal: string; confirmed: boolean }>(
        "/donor/me/recurring/resume", { method: "POST", headers: { "Idempotency-Key": key.current },
          body: JSON.stringify({ pause_id: data.pause_id, next_renewal: data.next_renewal }) });
      setData({ state: "saved", ...result }); setConfirm(false);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("Resumption is not confirmed. Retry the saved request or refresh its status.");
    } finally { setBusy(false); }
  }
  return <section className="panel">
    <h1>Resume monthly giving</h1>
    <p><Link href="/account/pause">My pause</Link></p>
    <p>Sandbox only. These controls affect test subscriptions.</p>
    {signedOut ? <Link href="/account/sign-in">Sign in to manage your giving</Link> : <>
      {!data && !error && <p role="status">Checking the next renewal…</p>}
      {data?.state === "unavailable" && <p>No pause is currently available to shorten. Giving may already resume at the next renewal.</p>}
      {data && data.state !== "unavailable" && <>
        {data.confirmed ? <p role="status">Resumption confirmed for <time dateTime={data.next_renewal}>{new Date(data.next_renewal).toLocaleString()}</time>.</p> : <>
          <p>Resume at your next regular renewal: <time dateTime={data.next_renewal}>{new Date(data.next_renewal).toLocaleString()}</time>.</p>
          {data.state === "saved" && <p role="status">Your resumption request is not confirmed yet.</p>}
          <p>This does not charge you immediately or collect renewals skipped during your pause.</p>
          {!confirm && data.state !== "saved" ? <button type="button" onClick={() => setConfirm(true)}>Resume at next renewal</button> : <>
            <h2>Confirm resumption</h2>
            <button type="button" disabled={busy} onClick={resume}>{busy ? "Checking resumption…" : "Confirm resume giving"}</button>
          </>}
        </>}
        <p>If you already skipped a renewal, your streak restarts only after a successful recurring charge.
          If no renewal was skipped, successful giving can continue the existing streak.</p>
      </>}
      {error && <p role="alert">{error}</p>}
      <button type="button" disabled={busy} onClick={() => setRetry((value) => value + 1)}>Refresh resumption status</button>
    </>}
  </section>;
}
