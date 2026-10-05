"use client";

import { useRef, useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, ApiError, unconfirmedMessage } from "@/lib/api";
import { formatMoney, MAX_GIFT_MINOR, MIN_GIFT_MINOR, monthlyFraming, parseDollars } from "@/lib/money";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { useStepFocus } from "@/lib/useStepFocus";

type Change = { request_id: string; amount_minor: number; previous_minor: number; effective_at: string; confirmed: boolean };
type Options = { state: "unavailable" } | { state: "ready"; subscription_id: string; amount_minor: number; effective_at: string; change: Change | null };

export default function Amount() {
  const [amount, setAmount] = useState("30");
  const [confirmation, setConfirmation] = useState<Change | null>(null);
  const [requestKey, setRequestKey] = useState<string | null>(null);
  const [signedOut, setSignedOut] = useState(false);
  const { resource, data, reload, setData } = useApiResource<Options>(signedOut ? null : "/donor/me/recurring/amount", {
    onLoad: (value) => {
      setRequestKey(null); setConfirmation(null); setError(""); setAmountInvalid(false);
      if (value.state !== "ready") return;
      setAmount((value.amount_minor / 100).toFixed(2));
      if (value.change && !value.change.confirmed) { setConfirmation(value.change); setRequestKey(value.change.request_id); }
    },
  });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [amountInvalid, setAmountInvalid] = useState(false);
  const [busy, setBusy] = useState(false);
  const amountInput = useRef<HTMLInputElement>(null);
  const step = useStepFocus(confirmation !== null);
  const messages = resourceMessages(resource, { loading: "Checking monthly giving…",
    failure: "We could not load amount changes. Finish any active pause and refresh your billing details." });

  function review(event: React.FormEvent) {
    event.preventDefault(); setError(""); setMessage(""); setAmountInvalid(false);
    if (data?.state !== "ready") return;
    const minor = parseDollars(amount);
    const problem = minor === null ? "Enter a dollar amount with up to two decimal places."
      : minor < MIN_GIFT_MINOR || minor > MAX_GIFT_MINOR || minor === data.amount_minor ? "Choose a different monthly amount between $1 and $999,999.99." : "";
    if (problem || minor === null) {
      setError(problem); setAmountInvalid(true); amountInput.current?.focus();
      return;
    }
    setConfirmation({ request_id: "", amount_minor: minor, previous_minor: data.amount_minor, effective_at: data.effective_at, confirmed: false });
  }
  async function save() {
    if (data?.state !== "ready" || !confirmation || busy) return;
    const key = requestKey ?? crypto.randomUUID();
    setRequestKey(key); setBusy(true); setError("");
    try {
      const result = await api<Change>("/donor/me/recurring/amount", { method: "POST",
        headers: { "Idempotency-Key": key }, body: JSON.stringify({ subscription_id: data.subscription_id,
          amount_minor: confirmation.amount_minor, previous_minor: confirmation.previous_minor, effective_at: confirmation.effective_at }) });
      setData({ ...data, amount_minor: result.amount_minor, change: result }); setConfirmation(null); setRequestKey(null);
      setAmount((result.amount_minor / 100).toFixed(2));
      if (result.confirmed) setMessage(`Last amount change confirmed: ${formatMoney(result.amount_minor)}/month from ${new Date(result.effective_at).toLocaleString()}.`);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError(unconfirmedMessage(cause, "Amount change is not confirmed. Retry the same request or refresh its status."));
    } finally { setBusy(false); }
  }
  const absent = signedOut || resource.state === "signedOut";
  return <section className="panel">
    <h1>Change monthly amount</h1>
    <p><Link href="/account/recurring">Manage monthly giving</Link></p>
    <p>Sandbox only. This changes future test gifts, with no charge or proration today.</p>
    {absent ? <p><Link href="/account/sign-in">Sign in to manage your giving</Link></p> : <>
      {data?.state === "unavailable" && <p>No active monthly gift is available to change.</p>}
      {data?.state === "ready" && <>
        <p>Next monthly amount: {formatMoney(data.amount_minor)}/month.</p>
        {data.change?.confirmed && !message && <p>Last amount change confirmed: {formatMoney(data.change.amount_minor)}/month from {new Date(data.change.effective_at).toLocaleString()}.</p>}
        {confirmation ? <div className="confirmStep">
          <h2 ref={step.heading} tabIndex={-1}>Confirm new monthly amount</h2>
          <p>{formatMoney(confirmation.previous_minor)}/month → {formatMoney(confirmation.amount_minor)}/month, starting {new Date(confirmation.effective_at).toLocaleString()}.</p>
          <p>Past gifts and outstanding invoices keep their original amounts. Your streak continues with successful monthly charges.</p>
          <button type="button" className="button" disabled={busy} onClick={save}>{busy ? "Checking change…" : "Confirm amount change"}</button>
          <button type="button" className="secondary" disabled={busy || requestKey !== null} onClick={() => setConfirmation(null)}>Back</button>
        </div> : <form onSubmit={review}>
          <div className="presets">{[3000, 9000, 15000].map((minor) => {
            const framing = monthlyFraming(minor);
            const value = (minor / 100).toFixed(2);
            return <button key={minor} type="button" aria-pressed={amount === value} onClick={() => { setAmount(value); setAmountInvalid(false); }}>
              <span>{framing.monthly}</span> · <span>{framing.daily}</span>
            </button>;
          })}</div>
          <label>Monthly amount in USD<input ref={amountInput} inputMode="decimal" value={amount} required
            aria-invalid={amountInvalid || undefined} aria-describedby={amountInvalid ? "amount-error" : undefined}
            onChange={(event) => { setAmount(event.target.value); setAmountInvalid(false); }} /></label>
          <button type="submit" ref={step.trigger} className="button">Review amount change</button>
        </form>}
      </>}
      <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh amount details</button>
    </>}
    <Announcer id="amount-error" status={absent ? "" : messages.status || message} alert={absent ? "" : error || messages.alert} />
  </section>;
}
