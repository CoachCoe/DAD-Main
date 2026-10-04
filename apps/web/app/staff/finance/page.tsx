"use client";
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";

type Quote = { gift_id: string; donor_id: string; amount_minor: number; available_minor: number } | {
  subscription: { id: string; amount_minor: number; period_end: string; stops_at: string | null } | null;
};
type Action = { id: string; actor_id: string; kind: string; target_id: string; amount_minor: number | null; period_end: string | null; reason: string; status: string };
type Confirmation = { amount_minor?: number; subscription_id?: string; period_end?: string; reason: string };
type Result = { status?: string; subscription?: { stops_at: string | null } };
const money = (minor: number) => new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" }).format(minor / 100);

export default function StaffFinancePage() {
  const [donor, setDonor] = useState("");
  const [gift, setGift] = useState("");
  const [quote, setQuote] = useState<Quote | null>(null);
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const key = useRef<string | null>(null);
  useEffect(() => {
    const params = new URL(window.location.href).searchParams;
    setDonor(params.get("donor") || ""); setGift(params.get("gift") || "");
  }, []);
  const path = `/donor/staff/donors/${encodeURIComponent(donor)}/${gift ? `gifts/${encodeURIComponent(gift)}/refund` : "cancellation"}`;
  useEffect(() => {
    if (!donor) return;
    let active = true;
    setError(""); setQuote(null); setConfirmation(null); key.current = null;
    Promise.all([api<Quote>(path), api<Action[]>(`/donor/staff/donors/${encodeURIComponent(donor)}/actions`), api<{ id: string }>("/donor/staff/me")]).then(([value, actions, actor]) => {
      if (!active) return;
      setQuote(value);
      if ("gift_id" in value) setAmount((value.available_minor / 100).toFixed(2));
      const target = "gift_id" in value ? value.gift_id : value.subscription?.id;
      const pending = actions.find((item) => item.target_id === target && item.status === "AUTHORIZED" && item.actor_id === actor.id);
      if (pending) {
        key.current = pending.id; setReason(pending.reason);
        setConfirmation(pending.kind === "REFUND" ? { amount_minor: pending.amount_minor!, reason: pending.reason } :
          { subscription_id: pending.target_id, period_end: pending.period_end!, reason: pending.reason });
      }
    }).catch(() => { if (active) setError("Financial details could not be loaded. Confirm Finance or Administrator access and retry when Stripe is available."); });
    return () => { active = false; };
  }, [donor, gift, path, retry]);
  function review(event: React.FormEvent) {
    event.preventDefault(); setError(""); setMessage("");
    if (!quote || reason.trim().length < 5) return;
    if ("gift_id" in quote) {
      if (!/^\d+(\.\d{1,2})?$/.test(amount)) { setError("Enter a refund in USD with at most two decimal places."); return; }
      const [dollars, cents = ""] = amount.split(".");
      const minor = Number(dollars) * 100 + Number(cents.padEnd(2, "0"));
      if (!Number.isSafeInteger(minor) || minor < 1 || minor > quote.available_minor) { setError("Choose an amount within the available refund balance."); return; }
      setConfirmation({ amount_minor: minor, reason: reason.trim() });
    } else if (quote.subscription) setConfirmation({ subscription_id: quote.subscription.id, period_end: quote.subscription.period_end, reason: reason.trim() });
  }
  async function submit() {
    if (!confirmation || busy) return;
    setBusy(true); setError(""); setMessage(""); key.current ??= crypto.randomUUID();
    try {
      const result = await api<Result>(path, { method: "POST", headers: { "Idempotency-Key": key.current }, body: JSON.stringify(confirmation) });
      setMessage(result.status === "NOT_SUBMITTED" ? "No refund submitted. The available amount changed. Refresh and confirm a new request." :
        result.status ? `Stripe refund status: ${result.status}. Only successful refunds reduce collected totals.` :
        `Cancellation confirmed at ${new Date(result.subscription!.stops_at!).toLocaleString()}. Past gifts are not refunded.`);
      setConfirmation(null); key.current = null;
    } catch { setError("This action is not confirmed. Retry the same request to reconcile it. Do not issue another refund while its outcome is unknown."); }
    finally { setBusy(false); }
  }
  return <section className="panel"><h1>{gift ? "Refund a gift" : "Cancel monthly giving"}</h1>
    <p><Link href="/staff">Staff back office</Link> · <Link href="/staff/sign-in">Staff sign-in</Link></p>
    <p>Sandbox only. A named financial permission and an audit reason are required.</p>
    {!donor && <p>Select a donor and gift in the staff back office.</p>}
    {donor && !quote && !error && <p role="status">Checking current Stripe records…</p>}
    {quote && <>{"gift_id" in quote ? <p>Original gift: {money(quote.amount_minor)}. Available to refund: {money(quote.available_minor)} USD.</p> :
      quote.subscription ? <p>{money(quote.subscription.amount_minor)}/month. Next renewal: {new Date(quote.subscription.period_end).toLocaleString()}.{quote.subscription.stops_at && " Cancellation is already scheduled."}</p> : <p>No monthly gift is available.</p>}
      {confirmation ? <div><h2>Confirm financial action</h2>
        <p>{confirmation.amount_minor !== undefined ? `Refund ${money(confirmation.amount_minor)} USD to the original payment method.` : `Stop future renewals at ${new Date(confirmation.period_end!).toLocaleString()}. No refund is included.`}</p>
        <p>Reason: {confirmation.reason}</p>
        <button disabled={busy} onClick={submit}>{busy ? "Checking action…" : key.current ? "Retry original financial action" : "Confirm financial action"}</button>
        <button disabled={busy || key.current !== null} onClick={() => setConfirmation(null)}>Back</button>
      </div> : (("gift_id" in quote && quote.available_minor > 0) || ("subscription" in quote && quote.subscription && !quote.subscription.stops_at)) && <form onSubmit={review}>
        {"gift_id" in quote && <label>Refund in USD<input inputMode="decimal" value={amount} onChange={(event) => setAmount(event.target.value)} required /></label>}
        <label>Audit reason<textarea minLength={5} maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} required /></label>
        <button disabled={busy} type="submit">Review financial action</button>
      </form>}
    </>}{message && <p role="status">{message}</p>}{error && <p role="alert">{error}</p>}
    {donor && <button disabled={busy} onClick={() => setRetry((value) => value + 1)}>Refresh financial details</button>}
  </section>;
}
