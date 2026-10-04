"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

type Change = { request_id: string; amount_minor: number; previous_minor: number; effective_at: string; confirmed: boolean };
type Options = { state: "unavailable" } | { state: "ready"; subscription_id: string; amount_minor: number; effective_at: string; change: Change | null };
const money = (minor: number) => new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" }).format(minor / 100);

export default function AmountPage() {
  const [data, setData] = useState<Options | null>(null);
  const [amount, setAmount] = useState("30");
  const [confirmation, setConfirmation] = useState<Change | null>(null);
  const [error, setError] = useState("");
  const [signedOut, setSignedOut] = useState(false);
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const key = useRef<string | null>(null);
  useEffect(() => {
    let active = true;
    setData(null); setError(""); setConfirmation(null);
    api<Options>("/donor/me/recurring/amount").then((value) => {
      if (!active) return;
      setData(value); key.current = null;
      if (value.state === "ready") {
        setAmount((value.amount_minor / 100).toFixed(2));
        if (value.change && !value.change.confirmed) { setConfirmation(value.change); key.current = value.change.request_id; }
      }
    }).catch((cause) => {
      if (!active) return;
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("We could not load amount changes. Finish any active pause and refresh your billing details.");
    });
    return () => { active = false; };
  }, [retry]);
  function review(event: React.FormEvent) {
    event.preventDefault(); setError("");
    if (!data || data.state !== "ready") return;
    if (!/^\d+(\.\d{1,2})?$/.test(amount)) { setError("Enter a dollar amount with up to two decimal places."); return; }
    const [dollars, cents = ""] = amount.split(".");
    const minor = Number(dollars) * 100 + Number(cents.padEnd(2, "0"));
    if (!Number.isSafeInteger(minor) || minor < 100 || minor > 99_999_999 || minor === data.amount_minor) {
      setError("Choose a different monthly amount between $1 and $999,999.99."); return;
    }
    setConfirmation({ request_id: "", amount_minor: minor, previous_minor: data.amount_minor, effective_at: data.effective_at, confirmed: false });
  }
  async function save() {
    if (!data || data.state !== "ready" || !confirmation || busy) return;
    key.current ??= crypto.randomUUID(); setBusy(true); setError("");
    try {
      const result = await api<Change>("/donor/me/recurring/amount", { method: "POST",
        headers: { "Idempotency-Key": key.current }, body: JSON.stringify({ subscription_id: data.subscription_id,
          amount_minor: confirmation.amount_minor, previous_minor: confirmation.previous_minor, effective_at: confirmation.effective_at }) });
      setData({ ...data, amount_minor: result.amount_minor, change: result }); setConfirmation(null); key.current = null;
      setAmount((result.amount_minor / 100).toFixed(2));
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("Amount change is not confirmed. Retry the same request or refresh its status.");
    } finally { setBusy(false); }
  }
  return <section className="panel">
    <h1>Change monthly amount</h1>
    <p><Link href="/account/recurring">Manage monthly giving</Link></p>
    <p>Sandbox only. This changes future test gifts, with no charge or proration today.</p>
    {signedOut ? <Link href="/account/sign-in">Sign in to manage your giving</Link> : <>
      {!data && !error && <p role="status">Checking monthly giving…</p>}
      {data?.state === "unavailable" && <p>No active monthly gift is available to change.</p>}
      {data?.state === "ready" && <>
        <p>Next monthly amount: {money(data.amount_minor)}/month.</p>
        {data.change?.confirmed && <p role="status">Last amount change confirmed: {money(data.change.amount_minor)}/month from {new Date(data.change.effective_at).toLocaleString()}.</p>}
        {confirmation ? <div>
          <h2>Confirm new monthly amount</h2>
          <p>{money(confirmation.previous_minor)}/month → {money(confirmation.amount_minor)}/month, starting {new Date(confirmation.effective_at).toLocaleString()}.</p>
          <p>Past gifts and outstanding invoices keep their original amounts. Your streak continues with successful monthly charges.</p>
          <button disabled={busy} onClick={save}>{busy ? "Checking change…" : "Confirm amount change"}</button>
          <button disabled={busy || key.current !== null} onClick={() => setConfirmation(null)}>Back</button>
        </div> : <form onSubmit={review}>
          <p>{[30, 90, 150].map((value) => <button key={value} type="button" onClick={() => setAmount(String(value))}>
            {money(value * 100)}/month · about ${value / 30}/day
          </button>)}</p>
          <label>Monthly amount in USD<input inputMode="decimal" value={amount} onChange={(event) => setAmount(event.target.value)} required /></label>
          <button type="submit">Review amount change</button>
        </form>}
      </>}
      {error && <p role="alert">{error}</p>}
      <button disabled={busy} onClick={() => setRetry((value) => value + 1)}>Refresh amount details</button>
    </>}
  </section>;
}
