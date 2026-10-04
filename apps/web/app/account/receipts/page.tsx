"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

type Gift = { id: string; amount_minor: number; refunded_minor: number; net_minor: number; paid_at: string; frequency: string;
  refunds: { amount_minor: number; status: string; observed_at: string }[] };
type Document = { as_of: string; receipt_id: string; gift: Gift } | {
  as_of: string; year: number; timezone: string; gifts: Gift[]; amount_minor: number; refunded_minor: number; net_minor: number;
};
const money = (minor: number) => new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" }).format(minor / 100);

export default function ReceiptsPage() {
  const [document, setDocument] = useState<Document | null>(null);
  const [error, setError] = useState("");
  const [signedOut, setSignedOut] = useState(false);
  const [year, setYear] = useState("2026");
  const [path, setPath] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const gift = new URL(window.location.href).searchParams.get("gift");
    if (gift) setPath(`/donor/me/receipts/${encodeURIComponent(gift)}`);
  }, []);
  useEffect(() => {
    if (!path) return;
    let active = true;
    setDocument(null); setError("");
    api<Document>(path).then((value) => { if (active) setDocument(value); }).catch((cause) => {
      if (!active) return;
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("This gift record could not be loaded. Check the year, your connection, and try again.");
    });
    return () => { active = false; };
  }, [path, retry]);
  return <section className="panel">
    <h1>Gift records and annual summaries</h1>
    <p><Link href="/account/giving">My giving</Link></p>
    <p><strong>Sandbox only — not a tax receipt. No real donation was collected.</strong></p>
    {signedOut ? <Link href="/account/sign-in">Sign in to view your gift records</Link> : <>
      <form onSubmit={(event) => { event.preventDefault(); setPath(`/donor/me/annual-summary/${encodeURIComponent(year)}`); setRetry((value) => value + 1); }}>
        <label>Calendar year<input type="number" min="2026" max="9998" value={year} onChange={(event) => setYear(event.target.value)} required /></label>
        <button type="submit">View annual summary</button>
      </form>
      {path && !document && !error && <p role="status">Loading gift records…</p>}
      {document && <>
        <p>As of <time dateTime={document.as_of}>{new Date(document.as_of).toLocaleString()}</time>.</p>
        {"gift" in document ? <>
          <h2>Test gift record</h2><p>Reference: {document.receipt_id}</p>
          <p>{money(document.gift.amount_minor)} · {document.gift.frequency === "MONTHLY" ? "Monthly" : "One time"} · {new Date(document.gift.paid_at).toLocaleString()}</p>
          <p>Successful refunds: {money(document.gift.refunded_minor)}. Net test amount: {money(document.gift.net_minor)}.</p>
          <ul>{document.gift.refunds.map((refund, index) => <li key={index}>
            {money(refund.amount_minor)} · {refund.status} · status observed {new Date(refund.observed_at).toLocaleString()}
          </li>)}</ul>
        </> : <>
          <h2>{document.year} annual test summary</h2>
          <p>Calendar year uses {document.timezone}. Refunds reflect the latest verified adjustments to these gifts, including adjustments observed in later years. This is not a tax deduction calculation.</p>
          <p>Collected: {money(document.amount_minor)}. Successful refunds: {money(document.refunded_minor)}. Net: {money(document.net_minor)}.</p>
          {document.gifts.length ? <ul>{document.gifts.map((gift) => <li key={gift.id}>
            <button onClick={() => setPath(`/donor/me/receipts/${gift.id}`)}>{new Date(gift.paid_at).toLocaleDateString()} · {money(gift.amount_minor)}</button>
            {" · "}Net {money(gift.net_minor)}
          </li>)}</ul> : <p>No confirmed gifts in this year.</p>}
        </>}
        <button onClick={() => window.print()}>Print test record</button>
      </>}
      {error && <p role="alert">{error}</p>}
      {path && <button onClick={() => setRetry((value) => value + 1)}>Refresh gift records</button>}
    </>}
  </section>;
}
