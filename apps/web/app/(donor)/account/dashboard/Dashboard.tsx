"use client";

import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { SignInPrompt } from "@/components/donor/SignInPrompt";
import type { Schema } from "@/lib/api";
import { formatMoney } from "@/lib/money";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { formatDateTime } from "@/lib/dates";

function LoadingMetric({ title, children, detail = false }: { title: string; children: React.ReactNode; detail?: boolean }) {
  return <article className="metric"><h2>{title}</h2>
    <p className="metricValue"><span className="metricSkeleton" /></p><p>{children}</p>
    {detail && <p className="metricSkeletonDetail"><span>View Link and IRL breakdown</span><span>+</span></p>}
  </article>;
}

export default function Dashboard() {
  const { resource, data, failed, reload } = useApiResource<Schema<"DonorDashboard">>("/donor/me/dashboard");
  const messages = resourceMessages(resource, { loading: "Loading your dashboard…",
    failure: "Your dashboard could not be loaded right now. Please retry in a moment." });
  const updating = data?.state === "preparing" ? "We’re preparing your dashboard. Please refresh shortly."
    : data?.state === "ready" && !data.referrals ? "Referral totals are updating. Please refresh shortly." : "";

  return <Page wide eyebrow="My account" title="Your circle’s impact">
    <p className="note">DAD launches in April 2027. This preview shows test payments and simulated recognition only.</p>
    {resource.state === "signedOut" ? <SignInPrompt>Sign in to view your dashboard</SignInPrompt> : <>
      {(resource.state === "loading" || data?.state === "preparing") && <>
        <div className="metrics pair" aria-hidden="true">
          <LoadingMetric title="Link Referrals">People who gave after opening your link.</LoadingMetric>
          <LoadingMetric title="IRL Referrals">People who gave after scanning your code.</LoadingMetric>
        </div>
        <div className="metrics" aria-hidden="true">
          <LoadingMetric title="Money raised because of you" detail>Collected test gifts after successful refunds.</LoadingMetric>
          <LoadingMetric title="Giving streak">Consecutive months with a successful monthly gift. One-time gifts don’t change it.</LoadingMetric>
          <LoadingMetric title="Recognition"><span className="metricSkeletonCopy">Your first gift came before launch. The badge is permanent. Sandbox recognition only.</span></LoadingMetric>
        </div>
      </>}
      {data?.state === "ready" && <>
        {data.streak?.at_risk && <div className="callout warn">
          <p>Your streak is at risk: your latest monthly payment didn’t go through.
            {data.streak.grace_ends_at && <> Update your card by <time dateTime={data.streak.grace_ends_at}>
              {formatDateTime(data.streak.grace_ends_at)}</time>, when the grace period ends, to keep your {data.streak.current}-month streak.</>}</p>
          <Link className="button" href="/account/payment-method">Update payment method</Link>
        </div>}
        <div className="metrics pair">
          <article className="metric"><h2>Link Referrals</h2><p className="metricValue">{data.referrals?.link ?? "Updating"}</p>
            <p>People who gave after opening your link.</p></article>
          <article className="metric"><h2>IRL Referrals</h2><p className="metricValue">{data.referrals?.irl ?? "Updating"}</p>
            <p>People who gave after scanning your code.</p></article>
        </div>
        <div className="metrics">
          <article className="metric"><h2>Money raised because of you</h2>
            <p className="metricValue">{data.referrals ? formatMoney(data.referrals.total_minor) : "Updating"}</p>
            <p>Collected test gifts after successful refunds.</p>
            {data.referrals && <details><summary>View Link and IRL breakdown</summary>
              <p>Link: {formatMoney(data.referrals.link_minor)}</p><p>IRL: {formatMoney(data.referrals.irl_minor)}</p>
            </details>}</article>
          <article className="metric"><h2>Giving streak</h2>
            <p className="metricValue">{data.streak ? `${data.streak.current} ${data.streak.current === 1 ? "month" : "months"}` : "Updating"}</p>
            {data.streak ? <p>Consecutive months with a successful monthly gift. One-time gifts don’t change it.</p>
              : <p>We’re checking your billing history. Your streak is temporarily unavailable.</p>}
          </article>
          {data.founding_donor && <article className="metric"><h2>Founding Donor</h2>
            <p className="metricValue">Earned</p>
            <p>Your first gift came before launch. The badge is permanent. Sandbox recognition only.</p></article>}
        </div>
        {!data.founding_donor && <p className="note">Founding Donor badge: not yet earned. A first gift completed before DAD launches on 22 April 2027 earns it permanently.</p>}
      </>}
      <Announcer status={messages.status || updating} alert={messages.alert} statusShownElsewhere={resource.state === "loading"} />
      {(failed || updating) && <button type="button" className="secondary" onClick={reload}>Refresh dashboard</button>}
      <nav aria-label="Account" className="links">
        <Link href="/account/share">Share and invite</Link>
        <Link href="/account/code">My referral code</Link>
        <Link href="/account/giving">My giving</Link>
        <Link href="/account/profile">Profile and notifications</Link>
      </nav>
    </>}
  </Page>;
}
