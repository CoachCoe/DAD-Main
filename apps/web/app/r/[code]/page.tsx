import type { Metadata } from "next";
import { headers } from "next/headers";
import { cache } from "react";
import { trustedClientIp } from "@/lib/clientIp";
import { donorApiBase } from "@/lib/donorApi";
import ReferralLanding, { type Referrer } from "./ReferralLanding";

// Shared by generateMetadata and the page so one visit costs one lookup.
const lookUp = cache(async (code: string): Promise<Referrer> => {
  const forwarded = new Headers({ accept: "application/json" });
  const client = trustedClientIp(await headers());
  if (client) forwarded.set("x-real-ip", client);
  try {
    const response = await fetch(`${donorApiBase()}/donor/referrals/${encodeURIComponent(code)}`, {
      cache: "no-store", headers: forwarded, signal: AbortSignal.timeout(3_000),
    });
    if (response.status === 404) return { state: "unavailable" };
    if (!response.ok) return { state: "unknown" };
    const { public_name: name } = await response.json();
    return typeof name === "string" && name.trim() ? { state: "found", name } : { state: "unknown" };
  } catch {
    // The browser's own capture request confirms the referral; the name can wait for it.
    return { state: "unknown" };
  }
});

export async function generateMetadata({ params }: PageProps<"/r/[code]">): Promise<Metadata> {
  const referrer = await lookUp((await params).code);
  const title = referrer.state === "found" ? `${referrer.name} invited you to DAD` : "You’re invited to DAD";
  const description = "Join with about a dollar a day — $30/month. DAD launches in April 2027.";
  return { title, description, openGraph: { title, description }, robots: { index: false } };
}

export default async function ReferralPage({ params, searchParams }: PageProps<"/r/[code]">) {
  const { code } = await params;
  const { via } = await searchParams;
  return <ReferralLanding code={code} kind={via === "irl" ? "IRL" : "LINK"} referrer={await lookUp(code)} />;
}
