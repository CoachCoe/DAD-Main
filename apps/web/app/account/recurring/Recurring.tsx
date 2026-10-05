"use client";

import { useRef, useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, ApiError, unconfirmedMessage } from "@/lib/api";
import { formatMoney } from "@/lib/money";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { useStepFocus } from "@/lib/useStepFocus";

type Recurring = { state: "ready" | "preparing"; subscription: {
  id: string; amount_minor: number; currency: string; status: string;
  period_end: string; stops_at: string | null;
} | null };

const date = (value: string) => new Date(value).toLocaleString();

export default function RecurringGiving() {
  const [signedOut, setSignedOut] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const command = useRef<{ key: string; subscription_id: string; period_end: string } | null>(null);
  const { resource, data, reload, setData } = useApiResource<Recurring>(signedOut ? null : "/donor/me/recurring", {
    onLoad: () => { setConfirming(false); setError(""); command.current = null; },
  });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const step = useStepFocus(confirming);
  const messages = resourceMessages(resource, { loading: "Checking your monthly gift…",
    failure: "We could not confirm your billing details right now. Please refresh in a moment." });

  async function cancel() {
    if (!data?.subscription || busy) return;
    command.current ??= { key: crypto.randomUUID(), subscription_id: data.subscription.id,
      period_end: data.subscription.period_end };
    const { key, ...body } = command.current;
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await api<Recurring>("/donor/me/recurring/cancel", {
        method: "POST", headers: { "Idempotency-Key": key }, body: JSON.stringify(body),
      });
      setData(result); setConfirming(false); command.current = null;
      if (result.subscription?.stops_at) setMessage(`Renewals cancelled. Your current billing period ends ${date(result.subscription.stops_at)}.`);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError(unconfirmedMessage(cause, "Cancellation is not confirmed. Retry to check its status, or refresh billing details before confirming again."));
    } finally { setBusy(false); }
  }
  const subscription = data?.subscription;
  const absent = signedOut || resource.state === "signedOut";
  const preparing = data?.state === "preparing" ? "Your checkout is still being confirmed. Refresh in a moment." : "";
  return <section className="panel">
    <h1>My monthly giving</h1>
    <p><Link href="/account/giving">Giving history</Link></p>
    <p>Sandbox only. These controls affect test subscriptions.</p>
    {absent ? <p><Link href="/account/sign-in">Sign in to manage your giving</Link></p> : <>
      {data?.state === "ready" && !subscription && <p>You have no monthly gift to manage.</p>}
      {subscription && <>
        <p className="donorMetricValue">{formatMoney(subscription.amount_minor, subscription.currency)}/month</p>
        {subscription.stops_at ? !message && <p>Renewals cancelled. Your current billing period ends {date(subscription.stops_at)}.</p> : <>
          <p>Current billing period ends <time dateTime={subscription.period_end}>{date(subscription.period_end)}</time>.</p>
          {!["active", "past_due", "unpaid"].includes(subscription.status) ? <p>This monthly gift is not ready for cancellation. Refresh once checkout finishes.</p>
            : confirming ? <div className="confirmStep">
              <h2 ref={step.heading} tabIndex={-1}>Confirm cancellation</h2>
              <p>Stop future renewals at {date(subscription.period_end)}? This does not refund past gifts or forgive an outstanding invoice.
                Your streak ends at the first skipped renewal. An earned Founding Donor badge stays yours.</p>
              <button type="button" className="secondary danger" disabled={busy} onClick={cancel}>{busy ? "Checking cancellation…" : "Confirm cancel renewals"}</button>
              <button type="button" className="secondary" disabled={busy} onClick={() => setConfirming(false)}>Back</button>
            </div> : null}
        </>}
        <nav aria-label="Manage monthly giving" className="accountLinks">
          <Link href="/account/payment-method">Update payment method</Link>
          <Link href="/account/pause">Pause monthly giving</Link>
          <Link href="/account/amount">Change monthly amount</Link>
        </nav>
        {!subscription.stops_at && !confirming && ["active", "past_due", "unpaid"].includes(subscription.status) &&
          <button type="button" ref={step.trigger} className="quiet" onClick={() => setConfirming(true)}>Cancel monthly giving</button>}
        <p>Tax receipts are not available in this preview yet.</p>
      </>}
      <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh billing details</button>
    </>}
    <Announcer status={absent ? "" : messages.status || message || preparing} alert={absent ? "" : error || messages.alert} />
  </section>;
}
