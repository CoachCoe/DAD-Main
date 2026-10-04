"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";

export default function ReferralLanding({ code }: { code: string }) {
  const [result, setResult] = useState<{ captured: boolean; public_name: string | null } | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setResult(null); setError("");
    const kind = new URLSearchParams(window.location.search).get("via") === "irl" ? "IRL" : "LINK";
    api<{ captured: boolean; public_name: string | null }>(`/donor/referrals/${encodeURIComponent(code)}`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ kind }),
      signal: controller.signal,
    }).then((value) => { if (!controller.signal.aborted) setResult(value); })
      .catch(() => { if (!controller.signal.aborted) setError("We couldn’t confirm your referral. Check your connection and retry."); });
    return () => controller.abort();
  }, [code, retry]);
  return <section className="panel authPanel">
    <span className="eyebrow">DAD · SANDBOX PREVIEW</span>
    <h1>{result?.public_name ? `${result.public_name} invited you to DAD` : "Join the beginning"}</h1>
    <p>DAD launches in April 2027. Join a growing circle of people giving together.</p>
    <p>About a dollar a day · $30/month. One-time giving is also available.</p>
    <p>A qualifying gift before launch earns a permanent Founding Donor badge.</p>
    <p>This is a sandbox preview. No real donation is collected or live badge awarded.</p>
    {!result && !error && <p role="status">Saving your referral…</p>}
    {result?.captured && <Link className="button" href="/donate">Continue to giving</Link>}
    {result && !result.captured && <><p>This referral link is no longer available.</p>
      <Link href="/donate">Continue without a referral</Link></>}
    {error && <><p role="alert">{error}</p><button onClick={() => setRetry((value) => value + 1)}>Retry referral</button>
      <p><Link href="/donate">Continue to giving anyway</Link></p></>}
  </section>;
}
