"use client";

import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, failureMessage } from "@/lib/api";
import { formatMoney } from "@/lib/money";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type Gifts = { email: string; next_offset: number | null; gifts: {
  id: string; amount_minor: number; currency: string; frequency: string; paid_at: string;
}[] };

export default function Giving() {
  const [offset, setOffset] = useState(0);
  const [signedOut, setSignedOut] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const { resource, data, reload } = useApiResource<Gifts>(signedOut ? null : `/donor/me/gifts?offset=${offset}`);
  const messages = resourceMessages(resource, { loading: "Loading your giving…",
    failure: "Giving history could not be loaded right now. Please retry in a moment." });
  async function logout() {
    setBusy(true); setError("");
    try { await api("/donor/logout", { method: "POST" }); setSignedOut(true); }
    catch (cause) { setError(failureMessage(cause, "Sign-out was not confirmed. Please retry.")); }
    finally { setBusy(false); }
  }
  return <section className="panel">
    <h1>My giving</h1>
    <p><Link href="/account/dashboard">My dashboard</Link></p>
    <p>Sandbox history only. These are simulated payments, not tax receipts. Records show verified refund adjustments. Legal tax receipts await activation.</p>
    {signedOut || resource.state === "signedOut" ? <p><Link href="/account/sign-in">Sign in to view your giving</Link></p> : <>
      {data && <>
        <p>{data.email}</p>
        <nav aria-label="Manage giving" className="accountLinks">
          <Link href="/account/recurring">Manage monthly giving</Link>
          <Link href="/account/payment-method">Update payment method</Link>
          <Link href="/account/receipts">Annual summaries</Link>
        </nav>
        <h2>Gifts</h2>
        {data.gifts.length === 0 ? <p>No confirmed test gifts yet. Payment confirmation may take a moment.</p> :
          <ul>{data.gifts.map((gift) => <li key={gift.id}>
            {formatMoney(gift.amount_minor, gift.currency)}
            {" · "}{gift.frequency === "MONTHLY" ? "Monthly" : "One time"}{" · "}
            <time dateTime={gift.paid_at}>{new Date(gift.paid_at).toLocaleDateString()}</time>{" · "}<Link href={`/account/receipts?gift=${gift.id}`}>Gift record</Link>
          </li>)}</ul>}
        {offset > 0 && <button type="button" className="secondary" onClick={() => setOffset(Math.max(0, offset - 50))}>Newer gifts</button>}
        {data.next_offset !== null && <button type="button" className="secondary" onClick={() => setOffset(data.next_offset ?? 0)}>Older gifts</button>}
      </>}
      <Announcer status={messages.status} alert={error || messages.alert} />
      <div className="actionRow">
        <button type="button" className="secondary" onClick={reload}>Refresh history</button>
        {data && <button type="button" className="quiet" disabled={busy} onClick={logout}>{busy ? "Signing out…" : "Sign out"}</button>}
      </div>
    </>}
  </section>;
}
