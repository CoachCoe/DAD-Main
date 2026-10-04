"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

type Update = { status: "NONE" | "PENDING" | "OPEN" | "APPLIED" | "EXPIRED" | "CANCELLED";
  completed_at: string | null };

export default function PaymentMethodPage() {
  const [data, setData] = useState<Update | null>(null);
  const [error, setError] = useState("");
  const [signedOut, setSignedOut] = useState(false);
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const key = useRef<string | null>(null);
  useEffect(() => {
    let active = true;
    setData(null); setError("");
    api<Update>("/donor/me/payment-method").then((value) => {
      if (active) { setData(value); key.current = null; }
    }).catch((cause) => {
      if (!active) return;
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("We could not check your payment-method update. Check your connection and refresh.");
    });
    return () => { active = false; };
  }, [retry]);
  async function begin() {
    if (busy) return;
    setBusy(true); setError("");
    key.current ??= crypto.randomUUID();
    try {
      const result = await api<{ url: string | null; status?: Update["status"]; completed_at?: string | null }>("/donor/me/payment-method/setup", {
        method: "POST", headers: { "Idempotency-Key": key.current },
      });
      if (result.url === null && (result.status === "APPLIED" || result.status === "CANCELLED")) {
        setData({ status: result.status, completed_at: result.completed_at ?? null });
        key.current = null;
        return;
      }
      if (!result.url) throw new Error("Missing checkout address");
      const url = new URL(result.url);
      if (url.protocol !== "https:" || url.host !== "checkout.stripe.com" || url.username || url.password) {
        throw new Error("Unexpected checkout address");
      }
      window.location.assign(url.href);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else if (cause instanceof ApiError && cause.status === 409) setError(cause.message);
      else setError("We could not open Stripe. Retry, or refresh to check whether the update has completed.");
    } finally { setBusy(false); }
  }
  return <section className="panel">
    <h1>Update payment method</h1>
    <p><Link href="/account/giving">My giving</Link></p>
    <p>Sandbox only. Stripe securely collects your test card for future monthly gifts.</p>
    {signedOut ? <>
      <p>Sign in with the email used for your gift. After claiming your account, choose Update payment method.</p>
      <Link href="/account/sign-in">Email me a sign-in link</Link>
    </> : <>
      {!data && !error && <p role="status">Checking update status…</p>}
      {data?.status === "APPLIED" && <p role="status">Your last payment-method update was confirmed
        {data.completed_at && <> on <time dateTime={data.completed_at}>{new Date(data.completed_at).toLocaleString()}</time></>}.</p>}
      {(data?.status === "PENDING" || data?.status === "OPEN") && <p role="status">Your update is not confirmed yet. You can continue the Stripe flow or refresh after completing it.</p>}
      {data?.status === "EXPIRED" && <p role="status">The Stripe session expired. Start a new update below.</p>}
      {data?.status === "CANCELLED" && <p role="status">The monthly gift ended before this update could be applied.</p>}
      {data && <>
        <p>Updating a card does not confirm payment of an overdue gift or restore your streak. Those change only after a successful charge.</p>
        <button disabled={busy} onClick={begin}>{busy ? "Opening Stripe…" : "Continue to Stripe"}</button>
      </>}
      {error && <p role="alert">{error}</p>}
      <button disabled={busy} onClick={() => setRetry((value) => value + 1)}>Refresh update status</button>
    </>}
  </section>;
}
