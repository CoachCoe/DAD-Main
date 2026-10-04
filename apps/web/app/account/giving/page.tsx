"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

type Gifts = { email: string; next_offset: number | null; gifts: {
  id: string; amount_minor: number; currency: string; frequency: string; paid_at: string;
}[] };

export default function GivingPage() {
  const [data, setData] = useState<Gifts | null>(null);
  const [error, setError] = useState("");
  const [signedOut, setSignedOut] = useState(false);
  const [busy, setBusy] = useState(false);
  const [offset, setOffset] = useState(0);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true;
    setData(null); setError("");
    api<Gifts>(`/donor/me/gifts?offset=${offset}`).then((result) => {
      if (active) setData(result);
    }).catch((cause) => {
      if (!active) return;
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("Giving history could not be loaded. Check your connection and retry.");
    });
    return () => { active = false; };
  }, [offset, retry]);
  async function logout() {
    setBusy(true);
    try { await api("/donor/logout", { method: "POST" }); setData(null); setSignedOut(true); }
    catch { setError("Sign-out failed. Please retry."); }
    finally { setBusy(false); }
  }
  return <section className="panel">
    <h1>My giving</h1>
    <p><Link href="/account/dashboard">My dashboard</Link></p>
    <p>Sandbox history only. These are simulated payments, not tax receipts. Refunds and tax receipts are not available in this preview.</p>
    {signedOut ? <Link href="/account/sign-in">Sign in to view your giving</Link> : <>
      {!data && !error && <p role="status">Loading your giving…</p>}
      {data && <>
        <p>{data.email}</p>
        <p><Link href="/account/recurring">Manage monthly giving</Link></p>
        {data.gifts.length === 0 ? <p>No confirmed test gifts yet. Payment confirmation may take a moment.</p> :
          <ul>{data.gifts.map((gift) => <li key={gift.id}>
            {new Intl.NumberFormat("en-US", { style: "currency", currency: gift.currency }).format(gift.amount_minor / 100)}
            {" · "}{gift.frequency === "MONTHLY" ? "Monthly" : "One time"}{" · "}
            <time dateTime={gift.paid_at}>{new Date(gift.paid_at).toLocaleDateString()}</time>
          </li>)}</ul>}
        {offset > 0 && <button onClick={() => setOffset(Math.max(0, offset - 50))}>Newer gifts</button>}
        {data.next_offset !== null && <button onClick={() => setOffset(data.next_offset!)}>Older gifts</button>}
        <p><button disabled={busy} onClick={logout}>{busy ? "Signing out…" : "Sign out"}</button></p>
      </>}
      {error && <p role="alert">{error}</p>}
      <button onClick={() => setRetry((value) => value + 1)}>Refresh history</button>
    </>}
  </section>;
}
