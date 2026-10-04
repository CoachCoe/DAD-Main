"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

type Recurring = { state: "ready" | "preparing"; subscription: {
  id: string; amount_minor: number; currency: string; status: string;
  period_end: string; stops_at: string | null;
} | null };

export default function RecurringPage() {
  const [data, setData] = useState<Recurring | null>(null);
  const [error, setError] = useState("");
  const [signedOut, setSignedOut] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const command = useRef<{ key: string; subscription_id: string; period_end: string } | null>(null);
  useEffect(() => {
    let active = true;
    setData(null); setError(""); setConfirm(false);
    api<Recurring>("/donor/me/recurring").then((value) => {
      if (active) { setData(value); command.current = null; }
    }).catch((cause) => {
      if (!active) return;
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("We could not confirm your billing details. Check your connection and refresh.");
    });
    return () => { active = false; };
  }, [retry]);
  async function cancel() {
    if (!data?.subscription || busy) return;
    command.current ??= { key: crypto.randomUUID(), subscription_id: data.subscription.id,
      period_end: data.subscription.period_end };
    const { key, ...body } = command.current;
    setBusy(true); setError("");
    try {
      const result = await api<Recurring>("/donor/me/recurring/cancel", {
        method: "POST", headers: { "Idempotency-Key": key }, body: JSON.stringify(body),
      });
      setData(result); setConfirm(false); command.current = null;
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("Cancellation is not confirmed. Retry to check its status, or refresh billing details before confirming again.");
    } finally { setBusy(false); }
  }
  const subscription = data?.subscription;
  const date = (value: string) => new Date(value).toLocaleString();
  return <section className="panel">
    <h1>My monthly giving</h1>
    <p><Link href="/account/giving">Giving history</Link></p>
    <p>Sandbox only. These controls affect test subscriptions.</p>
    {signedOut ? <Link href="/account/sign-in">Sign in to manage your giving</Link> : <>
      {!data && !error && <p role="status">Checking your monthly gift…</p>}
      {data?.state === "preparing" && <p role="status">Your checkout is still being confirmed. Refresh in a moment.</p>}
      {data?.state === "ready" && !subscription && <p>You have no monthly gift to manage.</p>}
      {subscription && <>
        <p>{new Intl.NumberFormat("en-US", { style: "currency", currency: subscription.currency })
          .format(subscription.amount_minor / 100)}/month</p>
        {subscription.stops_at ? <p role="status">Renewals cancelled. Your current billing period ends {date(subscription.stops_at)}.</p> : <>
          <p>Current billing period ends <time dateTime={subscription.period_end}>{date(subscription.period_end)}</time>.</p>
          {["active", "past_due", "unpaid"].includes(subscription.status) ? <>
            {!confirm ? <button onClick={() => setConfirm(true)}>Cancel monthly giving</button> : <div>
              <h2>Confirm cancellation</h2>
              <p>Stop future renewals at {date(subscription.period_end)}? This does not refund past gifts or forgive an outstanding invoice.
                Your streak ends at the first skipped renewal. An earned Founding Donor badge stays yours.</p>
              <button disabled={busy} onClick={cancel}>{busy ? "Checking cancellation…" : "Confirm cancel renewals"}</button>
              <button disabled={busy} onClick={() => { setConfirm(false); }}>Back</button>
            </div>}
          </> : <p>This monthly gift is not ready for cancellation. Refresh once checkout finishes.</p>}
        </>}
        <p>Pausing, amount changes, payment-method updates, and receipts are not available in this preview yet.</p>
      </>}
      {error && <p role="alert">{error}</p>}
      <button disabled={busy} onClick={() => setRetry((value) => value + 1)}>Refresh billing details</button>
    </>}
  </section>;
}
