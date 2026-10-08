"use client";

import { useRef, useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { SignInPrompt } from "@/components/donor/SignInPrompt";
import { api, ApiError, unconfirmedMessage } from "@/lib/api";
import { formatMoney } from "@/lib/money";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { useStepFocus } from "@/lib/useStepFocus";
import { formatDateTime } from "@/lib/dates";

type Recurring = { state: "ready" | "preparing"; observed_at?: string; subscription: {
  id: string; amount_minor: number; currency: string; status: string;
  period_end: string; stops_at: string | null;
} | null };

const cancellable = (status: string) => ["active", "past_due", "unpaid"].includes(status);

export default function RecurringGiving() {
  const [signedOut, setSignedOut] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const command = useRef<{ key: string; subscription_id: string; period_end: string } | null>(null);
  const { resource, data, failed, reload, setData } = useApiResource<Recurring>(signedOut ? null : "/donor/me/recurring", {
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
      if (result.subscription?.stops_at) setMessage(`Renewals cancelled. Your current billing period ends ${formatDateTime(result.subscription.stops_at)}.`);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError(unconfirmedMessage(cause, "Cancellation is not confirmed. Retry to check its status, or refresh billing details before confirming again."));
    } finally { setBusy(false); }
  }
  const subscription = data?.subscription;
  const preparing = data?.state === "preparing" ? "Your checkout is still being confirmed. Refresh in a moment." : "";
  return <Page eyebrow="My account" title="My monthly giving" back={{ href: "/account/giving", label: "My giving" }}>
    {signedOut || resource.state === "signedOut" ? <SignInPrompt>Sign in to manage your giving</SignInPrompt> : <>
      {data?.state === "ready" && !subscription && <div className="callout">
        <p>You have no monthly gift to manage.</p>
        <Link className="button" href="/donate">Start a monthly gift</Link>
      </div>}
      {subscription && <>
        <p className="metricValue">{formatMoney(subscription.amount_minor, subscription.currency)}/month</p>
        {subscription.stops_at ? !message && <p>Renewals cancelled. Your current billing period ends {formatDateTime(subscription.stops_at)}.</p>
          : <p>Current billing period ends <time dateTime={subscription.period_end}>{formatDateTime(subscription.period_end)}</time>.</p>}
        {data?.observed_at && <p className="note">Last updated from Stripe <time dateTime={data.observed_at}>{formatDateTime(data.observed_at)}</time>.</p>}
        {!subscription.stops_at && !cancellable(subscription.status) && <p>This monthly gift is not ready for cancellation. Refresh once checkout finishes.</p>}
        <nav aria-label="Manage monthly giving" className="links">
          <Link href="/account/payment-method">Update payment method</Link>
          <Link href="/account/pause">Pause monthly giving</Link>
          <Link href="/account/amount">Change monthly amount</Link>
        </nav>
        {!subscription.stops_at && cancellable(subscription.status) && (confirming ? <div className="callout">
          <h2 ref={step.heading} tabIndex={-1}>Confirm cancellation</h2>
          <p>Stop future renewals at {formatDateTime(subscription.period_end)}? This does not refund past gifts or forgive an outstanding invoice.
            Your streak ends at the first skipped renewal. An earned Founding Donor badge stays yours.</p>
          <div className="actions">
            <button type="button" className="secondary danger" disabled={busy} onClick={cancel}>{busy ? "Checking cancellation…" : "Confirm cancel renewals"}</button>
            <button type="button" className="secondary" disabled={busy} onClick={() => setConfirming(false)}>Back</button>
          </div>
        </div> : <button type="button" ref={step.trigger} className="quiet" onClick={() => setConfirming(true)}>Cancel monthly giving</button>)}
      </>}
      <p className="note">Sandbox only. These controls affect test subscriptions. Tax receipts are not available in this preview yet.</p>
      <Announcer status={messages.status || message || preparing} alert={error || messages.alert} />
      {(failed || error || preparing || (subscription && !cancellable(subscription.status))) &&
        <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh billing details</button>}
    </>}
  </Page>;
}
