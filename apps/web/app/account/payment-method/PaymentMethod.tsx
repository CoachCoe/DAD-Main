"use client";

import { useRef, useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, ApiError, unconfirmedMessage } from "@/lib/api";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type Update = { status: "NONE" | "PENDING" | "OPEN" | "APPLIED" | "EXPIRED" | "CANCELLED"; completed_at: string | null };

const states: Partial<Record<Update["status"], string>> = {
  PENDING: "Your update is not confirmed yet. You can continue the Stripe flow or refresh after completing it.",
  OPEN: "Your update is not confirmed yet. You can continue the Stripe flow or refresh after completing it.",
  EXPIRED: "The Stripe session expired. Start a new update below.",
  CANCELLED: "The monthly gift ended before this update could be applied.",
};

export default function PaymentMethod() {
  const [signedOut, setSignedOut] = useState(false);
  const key = useRef<string | null>(null);
  const { resource, data, reload, setData } = useApiResource<Update>(signedOut ? null : "/donor/me/payment-method", {
    onLoad: () => { key.current = null; setError(""); },
  });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const messages = resourceMessages(resource, { loading: "Checking update status…",
    failure: "We could not check your payment-method update right now. Please refresh in a moment." });
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
      const url = new URL(result.url ?? "about:blank");
      if (url.protocol !== "https:" || url.host !== "checkout.stripe.com" || url.username || url.password) {
        setError("We could not open Stripe: it returned an unexpected address. Retry, or refresh to check whether the update has completed.");
        return;
      }
      window.location.assign(url.href);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError(unconfirmedMessage(cause, "We could not open Stripe. Retry, or refresh to check whether the update has completed."));
    } finally { setBusy(false); }
  }
  const absent = signedOut || resource.state === "signedOut";
  return <section className="panel">
    <h1>Update payment method</h1>
    <p><Link href="/account/giving">My giving</Link></p>
    <p>Sandbox only. Stripe securely collects your test card for future monthly gifts.</p>
    {absent ? <>
      <p>Sign in with the email used for your gift. After claiming your account, choose Update payment method.</p>
      <p><Link className="button" href="/account/sign-in">Email me a sign-in link</Link></p>
    </> : <>
      {data?.status === "APPLIED" && <p>Your last payment-method update was confirmed
        {data.completed_at && <> on <time dateTime={data.completed_at}>{new Date(data.completed_at).toLocaleString()}</time></>}.</p>}
      {data && states[data.status] && <p>{states[data.status]}</p>}
      {data && <>
        <p>Updating a card does not confirm payment of an overdue gift or restore your streak. Those change only after a successful charge.</p>
        <button type="button" className="button" disabled={busy} onClick={begin}>{busy ? "Opening Stripe…" : "Continue to Stripe"}</button>
      </>}
      <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh update status</button>
    </>}
    <Announcer status={absent ? "" : messages.status} alert={absent ? "" : error || messages.alert} />
  </section>;
}
