"use client";

import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { formatMoney } from "@/lib/money";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type Dashboard = { mode: "test"; state: "preparing" } | {
  mode: "test"; state: "ready"; computed_at: string; founding_donor: boolean;
  referrals: { link: number; irl: number; total_minor: number; link_minor: number;
    irl_minor: number; currency: string } | null;
  streak: { current: number; at_risk: boolean; grace_ends_at: string | null } | null;
};

export default function Dashboard() {
  const { resource, data, reload } = useApiResource<Dashboard>("/donor/me/dashboard");
  const messages = resourceMessages(resource, { loading: "Loading your dashboard…",
    failure: "Your dashboard could not be loaded right now. Please retry in a moment." });
  const updating = data?.state === "preparing" ? "We’re preparing your dashboard. Please refresh shortly."
    : data?.state === "ready" && !data.referrals ? "Referral totals are updating. Please refresh shortly." : "";

  return <section className="panel">
    <span className="eyebrow">DAD · SANDBOX PREVIEW</span>
    <h1>Your circle’s impact</h1>
    <p>DAD launches in April 2027. This preview shows test payments and simulated recognition only.</p>
    {resource.state === "signedOut" ? <p><Link href="/account/sign-in">Sign in to view your dashboard</Link></p> : <>
      {data?.state === "ready" && <>
        <div className="metricPair">
          <article className="metricCard"><h2>Link Referrals</h2><p className="donorMetricValue">{data.referrals?.link ?? "Updating"}</p>
            <p>People who gave after opening your link.</p></article>
          <article className="metricCard"><h2>IRL Referrals</h2><p className="donorMetricValue">{data.referrals?.irl ?? "Updating"}</p>
            <p>People who gave after scanning your code.</p></article>
        </div>
        <div className="metricRow">
          <article className="metricCard"><h2>Money raised because of you</h2>
            <p className="donorMetricValue">{data.referrals ? formatMoney(data.referrals.total_minor) : "Updating"}</p>
            <p>Collected test gifts after successful refunds.</p>
            {data.referrals && <details><summary>View Link and IRL breakdown</summary>
              <p>Link: {formatMoney(data.referrals.link_minor)}</p><p>IRL: {formatMoney(data.referrals.irl_minor)}</p>
            </details>}</article>
          <article className="metricCard"><h2>Giving streak</h2>
            <p className="donorMetricValue">{data.streak ? `${data.streak.current} ${data.streak.current === 1 ? "month" : "months"}` : "Updating"}</p>
            {data.streak ? <p>Consecutive months with a successful monthly gift. One-time gifts don’t change it.</p>
              : <p>We’re checking your billing history. Your streak is temporarily unavailable.</p>}
            {data.streak?.at_risk && <p className="warning">Your streak is at risk: your latest monthly payment didn’t go through.
              {data.streak.grace_ends_at && <> Grace ends <time dateTime={data.streak.grace_ends_at}>
                {new Date(data.streak.grace_ends_at).toLocaleString()}</time>.</>}{" "}
              <Link href="/account/payment-method">Update payment method</Link></p>}
          </article>
          {data.founding_donor && <article className="metricCard"><h2>Founding Donor</h2>
            <p className="donorMetricValue">Earned</p>
            <p>Your first gift came before launch. The badge is permanent. Sandbox recognition only.</p></article>}
        </div>
        {!data.founding_donor && <p className="badgeNote">Founding Donor badge: not yet earned. A first gift completed before DAD launches on 22 April 2027 earns it permanently.</p>}
      </>}
      <Announcer status={messages.status || updating} alert={messages.alert} />
      <div className="actionRow">
        <button type="button" className="secondary" onClick={reload}>Refresh dashboard</button>
      </div>
      <nav aria-label="Account" className="accountLinks">
        <Link href="/account/share">Share and invite</Link>
        <Link href="/account/code">My referral code</Link>
        <Link href="/account/giving">My giving and sign-out</Link>
        <Link href="/account/profile">Profile and notifications</Link>
      </nav>
    </>}
  </section>;
}
