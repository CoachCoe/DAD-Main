"use client";

import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { formatMoney } from "@/lib/money";
import { FIRST_SUMMARY_YEAR } from "@/lib/taxYear";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type Gift = { id: string; amount_minor: number; refunded_minor: number; net_minor: number; paid_at: string; frequency: string;
  refunds: { id: string; amount_minor: number; status: string; observed_at: string }[] };
type Document = { as_of: string; receipt_id: string; gift: Gift } | {
  as_of: string; year: number; timezone: string; gifts: Gift[]; amount_minor: number; refunded_minor: number; net_minor: number;
};

export default function Receipts({ gift, defaultYear, latestYear }: { gift?: string; defaultYear: number; latestYear: number }) {
  const [year, setYear] = useState(String(defaultYear));
  const [path, setPath] = useState<string | null>(gift ? `/donor/me/receipts/${encodeURIComponent(gift)}` : null);
  const { resource, data: document, reload } = useApiResource<Document>(path);
  const messages = resourceMessages(resource, { loading: "Loading gift records…",
    failure: "This gift record could not be loaded. Check the year and try again in a moment." });
  const active = path !== null;
  return <section className="panel">
    <h1>Gift records and annual summaries</h1>
    <p><Link href="/account/giving">My giving</Link></p>
    <p><strong>Sandbox only — not a tax receipt. No real donation was collected.</strong></p>
    {resource.state === "signedOut" && active ? <p><Link href="/account/sign-in">Sign in to view your gift records</Link></p> : <>
      <form onSubmit={(event) => {
        event.preventDefault();
        const next = `/donor/me/annual-summary/${encodeURIComponent(year)}`;
        if (next === path) reload(); else setPath(next);
      }}>
        <label>Calendar year<input type="number" min={FIRST_SUMMARY_YEAR} max={latestYear} value={year} onChange={(event) => setYear(event.target.value)} required /></label>
        <button type="submit" className="button">View annual summary</button>
      </form>
      {document && <>
        <p>As of <time dateTime={document.as_of}>{new Date(document.as_of).toLocaleString()}</time>.</p>
        {"gift" in document ? <>
          <h2>Test gift record</h2><p>Reference: {document.receipt_id}</p>
          <p>{formatMoney(document.gift.amount_minor)} · {document.gift.frequency === "MONTHLY" ? "Monthly" : "One time"} · {new Date(document.gift.paid_at).toLocaleString()}</p>
          <p>Successful refunds: {formatMoney(document.gift.refunded_minor)}. Net test amount: {formatMoney(document.gift.net_minor)}.</p>
          <ul>{document.gift.refunds.map((refund) => <li key={refund.id}>
            {formatMoney(refund.amount_minor)} · {refund.status} · status observed {new Date(refund.observed_at).toLocaleString()}
          </li>)}</ul>
        </> : <>
          <h2>{document.year} annual test summary</h2>
          <p>Calendar year uses {document.timezone}. Refunds reflect the latest verified adjustments to these gifts, including adjustments observed in later years. This is not a tax deduction calculation.</p>
          <p>Collected: {formatMoney(document.amount_minor)}. Successful refunds: {formatMoney(document.refunded_minor)}. Net: {formatMoney(document.net_minor)}.</p>
          {document.gifts.length ? <ul>{document.gifts.map((item) => <li key={item.id}>
            <button type="button" className="quiet" onClick={() => setPath(`/donor/me/receipts/${encodeURIComponent(item.id)}`)}>{new Date(item.paid_at).toLocaleDateString()} · {formatMoney(item.amount_minor)}</button>
            {" · "}Net {formatMoney(item.net_minor)}
          </li>)}</ul> : <p>No confirmed gifts in this year.</p>}
        </>}
        <button type="button" className="secondary" onClick={() => window.print()}>Print test record</button>
      </>}
      {active && <button type="button" className="secondary" onClick={reload}>Refresh gift records</button>}
    </>}
    <Announcer status={active ? messages.status : ""} alert={active ? messages.alert : ""} />
  </section>;
}
