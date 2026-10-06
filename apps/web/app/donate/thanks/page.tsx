import type { Metadata } from "next";
import Link from "next/link";
import { GiftFraming } from "@/components/donor/GiftFraming";
import { Page } from "@/components/donor/Page";
import { MAX_GIFT_MINOR, MIN_GIFT_MINOR } from "@/lib/money";

export const metadata: Metadata = { title: "Thank you", robots: { index: false } };

type Gift = { frequency: "MONTHLY" | "ONE_TIME"; minor: number };

// The checkout success URL carries what the donor chose. It is display-only: anything
// malformed falls back to a thank-you that restates nothing.
function giftFrom(params: Record<string, string | string[] | undefined>): Gift | null {
  const { amount_minor: amount, frequency } = params;
  if (typeof amount !== "string" || !/^\d{1,9}$/.test(amount)) return null;
  if (frequency !== "MONTHLY" && frequency !== "ONE_TIME") return null;
  const minor = Number(amount);
  return minor >= MIN_GIFT_MINOR && minor <= MAX_GIFT_MINOR ? { frequency, minor } : null;
}

export default async function ThankYouPage({ searchParams }: PageProps<"/donate/thanks">) {
  const gift = giftFrom(await searchParams);
  return <Page eyebrow="Gift received" title="Thank you for joining the beginning">
    {gift && <p>Your gift: <GiftFraming minor={gift.minor} monthly={gift.frequency === "MONTHLY"} /></p>}
    <p>DAD launches in April 2027. A first gift completed before launch earns a permanent Founding Donor badge.</p>
    <div className="callout highlight">
      <h2>Next: claim your account</h2>
      <p>Your account holds your giving streak and your personal referral link and code, so the people you invite count toward your circle.</p>
      <p>Once Stripe confirms your gift, we’ll email a claim link to the address you gave. If it hasn’t arrived, request a new one.</p>
      {/* A full page load: account pages need their own per-request security policy. */}
      <a className="button" href="/account/sign-in">Claim your account</a>
    </div>
    <p className="note">Your receipt email follows once Stripe confirms the payment. In this sandbox preview it is a test gift record, not a tax receipt, and no real donation is collected.</p>
    <Link className="secondary" href="/donate">Make another gift</Link>
  </Page>;
}
