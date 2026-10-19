import type { Metadata } from "next";
import { cache } from "react";
import type { ResponseBody } from "@/lib/api";
import { donorApiBase } from "@/lib/donorApi";
import ReferralLanding, { type Referrer } from "./ReferralLanding";

// Shared by generateMetadata and the page so one visit costs one lookup. A found name is
// also shared across visitors for a minute, so a viral link costs the API one lookup a
// minute rather than one a visit; Next caches only 200s, so unknown codes are not stored.
// The request carries no per-visitor header, which would split the cache by visitor.
const lookUp = cache(async (code: string): Promise<Referrer> => {
  try {
    const response = await fetch(`${donorApiBase()}/donor/referrals/${encodeURIComponent(code)}`, {
      headers: { accept: "application/json" },
      next: { revalidate: 60 },
      signal: AbortSignal.timeout(3_000),
    });
    if (response.status === 404) return { state: "unavailable" };
    if (!response.ok) return { state: "unknown" };
    const { public_name: name }: ResponseBody<"/donor/referrals/{code}", "get"> = await response.json();
    return typeof name === "string" && name.trim() ? { state: "found", name } : { state: "unknown" };
  } catch {
    // The browser's own capture request confirms the referral; the name can wait for it.
    return { state: "unknown" };
  }
});

export async function generateMetadata({ params }: PageProps<"/r/[code]">): Promise<Metadata> {
  const referrer = await lookUp(decoded((await params).code));
  const title = referrer.state === "found" ? `${referrer.name} invited you to DAD` : "You’re invited to DAD";
  const description = "Join with about a dollar a day — $30/month. DAD launches in April 2027.";
  return { title, description, openGraph: { title, description }, robots: { index: false } };
}

// Route params arrive still percent-encoded; every consumer encodes again, so decode once here.
function decoded(code: string) {
  try {
    return decodeURIComponent(code);
  } catch {
    return code;
  }
}

export default async function ReferralPage({ params, searchParams }: PageProps<"/r/[code]">) {
  const code = decoded((await params).code);
  const { via } = await searchParams;
  return <ReferralLanding code={code} kind={via === "irl" ? "IRL" : "LINK"} referrer={await lookUp(code)} />;
}
