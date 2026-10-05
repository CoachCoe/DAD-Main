"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, classifyFailure } from "@/lib/api";

export type Referrer = { state: "found"; name: string } | { state: "unavailable" } | { state: "unknown" };
type Capture = { captured: boolean; public_name: string | null };

const failures = {
  offline: "We couldn’t confirm your referral because you appear to be offline. Check your connection, then retry.",
  rateLimited: "We couldn’t confirm your referral: this connection has opened too many referral links. Wait a minute, then retry.",
  other: "We couldn’t confirm your referral. Please retry in a moment.",
};

export default function ReferralLanding({ code, kind, referrer = { state: "unknown" } }: {
  code: string; kind: "LINK" | "IRL"; referrer?: Referrer;
}) {
  const [result, setResult] = useState<Capture | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  // biome-ignore lint/correctness/useExhaustiveDependencies: retry is the retry trigger, deliberately unread
  useEffect(() => {
    const controller = new AbortController();
    setResult(null); setError("");
    // The capture sets the attribution cookie, so it must come from the visitor's browser.
    api<Capture>(`/donor/referrals/${encodeURIComponent(code)}`, {
      method: "POST", body: JSON.stringify({ kind }), signal: controller.signal,
    }).then((value) => { if (!controller.signal.aborted) setResult(value); })
      .catch((cause) => {
        if (controller.signal.aborted) return;
        const failure = classifyFailure(cause);
        setError(failure === "offline" ? failures.offline : failure === "rateLimited" ? failures.rateLimited : failures.other);
      });
    return () => controller.abort();
  }, [code, kind, retry]);
  // Always name the owner of the opened code. The capture's public_name describes an
  // earlier first-touch credit, which stays with that referrer and is not this page's to show.
  const name = referrer.state === "found" ? referrer.name : null;
  const unavailable = referrer.state === "unavailable" || (referrer.state === "unknown" && result?.captured === false);
  return <div className="authLayout wide"><section className="panel authPanel">
    <span className="eyebrow">DAD · SANDBOX PREVIEW</span>
    {unavailable ? <>
      <h1>This referral link is no longer available</h1>
      <p>The person who shared it may have replaced it with a new link. You can still give without a referral.</p>
      <p><Link className="button" href="/donate">Continue without a referral</Link></p>
    </> : <h1>{name ? `${name} invited you to DAD` : "Join the beginning"}</h1>}
    <p>DAD launches in April 2027. Join a growing circle of people giving together.</p>
    <p>About a dollar a day · $30/month. One-time giving is also available.</p>
    <p>A qualifying gift before launch earns a permanent Founding Donor badge.</p>
    <p>This is a sandbox preview. No real donation is collected or live badge awarded.</p>
    <Announcer status={!result && !error ? "Saving your referral…" : ""} alert={error} />
    {result && !unavailable && <p><Link className="button" href="/donate">Continue to giving</Link></p>}
    {error && <div className="actionRow">
      <button type="button" className="button" onClick={() => setRetry((value) => value + 1)}>Retry referral</button>
      <Link className="secondary" href="/donate">Continue to giving anyway</Link>
    </div>}
  </section></div>;
}
