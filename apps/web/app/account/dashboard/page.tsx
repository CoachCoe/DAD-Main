"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

type Dashboard = { mode: "test"; state: "preparing" } | {
  mode: "test"; state: "ready"; computed_at: string; founding_donor: boolean;
  referrals: { link: number; irl: number; total_minor: number; link_minor: number;
    irl_minor: number; currency: string } | null;
  streak: { current: number; at_risk: boolean; grace_ends_at: string | null } | null;
};
const money = (minor: number) => new Intl.NumberFormat("en-US", {
  style: "currency", currency: "USD",
}).format(minor / 100);

export default function DashboardPage() {
  const [data, setData] = useState<Dashboard | null>(null);
  const [error, setError] = useState("");
  const [signedOut, setSignedOut] = useState(false);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setData(null); setError(""); setSignedOut(false);
    api<Dashboard>("/donor/me/dashboard", { cache: retry > 0 ? "reload" : "default",  signal: controller.signal }).then((result) => {
      if (!controller.signal.aborted) setData(result);
    }).catch((cause) => {
      if (controller.signal.aborted) return;
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("Your dashboard could not be loaded. Check your connection and retry.");
    });
    return () => controller.abort();
  }, [retry]);

  return <section className="panel">
    <span className="eyebrow">DAD · SANDBOX PREVIEW</span>
    <h1>Your circle’s impact</h1>
    <p>DAD launches in April 2027. This preview shows test payments and simulated recognition only.</p>
    {signedOut ? <Link href="/account/sign-in">Sign in to view your dashboard</Link> : <>
      {!data && !error && <p role="status">Loading your dashboard…</p>}
      {error && <p role="alert">{error}</p>}
      {data?.state === "preparing" && <p role="status">We’re preparing your dashboard. Please refresh shortly.</p>}
      {data?.state === "ready" && <>
        <div className="donorMetrics">
          <article><h2>Link Referrals</h2><p className="donorMetricValue">{data.referrals?.link ?? "Updating"}</p>
            <p>People who gave after opening your link.</p></article>
          <article><h2>IRL Referrals</h2><p className="donorMetricValue">{data.referrals?.irl ?? "Updating"}</p>
            <p>People who gave after scanning your code.</p></article>
          <article><h2>Money raised because of you</h2>
            <p className="donorMetricValue">{data.referrals ? money(data.referrals.total_minor) : "Updating"}</p>
            <p>Collected test gifts after successful refunds.</p>
            {data.referrals && <details><summary>View Link and IRL breakdown</summary>
              <p>Link: {money(data.referrals.link_minor)}</p><p>IRL: {money(data.referrals.irl_minor)}</p>
            </details>}</article>
          <article><h2>Giving streak</h2>
            <p className="donorMetricValue">{data.streak ? `${data.streak.current} ${data.streak.current === 1 ? "month" : "months"}` : "Updating"}</p>
            {!data.streak && <p>We’re checking your billing history. Your streak is temporarily unavailable.</p>}
            {data.streak?.at_risk && <p role="status">Your streak is at risk.
              {data.streak.grace_ends_at && <> Grace ends <time dateTime={data.streak.grace_ends_at}>
                {new Date(data.streak.grace_ends_at).toLocaleString()}</time>.</>}</p>}
          </article>
          <article><h2>Founding Donor</h2>
            <p>{data.founding_donor ? "Earned in this sandbox" : "Not yet earned in this sandbox"}</p>
            <p>A qualifying first gift before launch earns a permanent badge.</p></article>
        </div>
        {!data.referrals && <p role="status">Referral totals are updating. Please refresh shortly.</p>}
      </>}
      <div className="uploadActions">
        <button type="button" onClick={() => setRetry((value) => value + 1)}>Refresh dashboard</button>
        <Link href="/account/giving">My giving and sign-out</Link>
        <Link href="/account/share">Share and invite</Link>
        <Link href="/account/code">My referral code</Link>
        <Link href="/account/profile">Profile and notifications</Link>
      </div>
    </>}
  </section>;
}
