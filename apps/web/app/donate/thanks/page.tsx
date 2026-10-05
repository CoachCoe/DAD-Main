import type { Metadata } from "next";
import Link from "next/link";
import { formatDollars, MAX_GIFT_MINOR, MIN_GIFT_MINOR, monthlyFraming } from "@/lib/money";

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
  const framing = gift?.frequency === "MONTHLY" ? monthlyFraming(gift.minor) : null;
  return (
    <div className="authLayout wide">
      <section className="panel authPanel">
        <span className="eyebrow">DAD · TEST CHECKOUT</span>
        <h1>Thank you for joining the beginning</h1>
        {gift && <p className="giftSummary">Your gift:{" "}
          {framing ? <><strong>{framing.monthly}</strong> · <strong>{framing.daily}</strong></>
            : <strong>{formatDollars(gift.minor)} once</strong>}</p>}
        <p>DAD launches in April 2027. A first gift completed before launch earns a permanent Founding Donor badge.</p>
        <p>Your receipt email follows once Stripe confirms the payment. In this sandbox preview it is a test gift record, not a tax receipt, and no real donation is collected.</p>
        <div className="claimCallout">
          <h2>Claim your account</h2>
          <p>Claiming gives you your giving streak and your personal referral link and code, so the people you invite count toward your circle.
            Once your gift is confirmed, we’ll email a claim link to the address you gave. If it hasn’t arrived, request a new one.</p>
          <Link className="button" href="/account/sign-in">Claim your account</Link>
        </div>
        <p><Link className="secondary" href="/donate">Make another gift</Link></p>
      </section>
    </div>
  );
}
