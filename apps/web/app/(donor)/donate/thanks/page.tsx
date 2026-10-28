import type { Metadata } from "next";
import { GiftFraming } from "@/components/donor/GiftFraming";
import { Page } from "@/components/donor/Page";
import { isGiftAmount } from "@/lib/money";

export const metadata: Metadata = { title: "Thank you", robots: { index: false } };

// The checkout success URL carries what the donor chose. It is display-only: anything
// malformed falls back to a thank-you that restates nothing.
function giftFrom(params: Record<string, string | string[] | undefined>): number | null {
  const { amount_minor: amount, frequency } = params;
  if (typeof amount !== "string" || !/^\d{1,9}$/.test(amount) || frequency !== "MONTHLY") return null;
  const minor = Number(amount);
  return isGiftAmount(minor) ? minor : null;
}

export default async function ThankYouPage({ searchParams }: PageProps<"/donate/thanks">) {
  const gift = giftFrom(await searchParams);
  return (
    <Page eyebrow="Your gift" title="Thank you for joining the beginning">
      {gift !== null && (
        <p>
          Your gift: <GiftFraming minor={gift} />
        </p>
      )}
      <p>
        DAD launches in April 2027. A first gift completed before January 2027 earns a permanent Founding Donor badge.
      </p>
      <div className="callout highlight">
        <h2>Next: claim your account</h2>
        <p>
          Your account holds your giving streak and your personal referral link and code, so the people you invite count
          toward your circle.
        </p>
        <p>
          Once Stripe confirms your gift, we’ll email a claim link to the address you gave. If it hasn’t arrived,
          request a new one.
        </p>
        {/* A full page load: account pages need their own per-request security policy. */}
        <a className="button" href="/account/sign-in">
          Claim your account
        </a>
      </div>
      <p className="note">
        Your receipt email follows once Stripe confirms the payment. In this sandbox preview it is a test gift record,
        not a tax receipt, and no real donation is collected.
      </p>
      <a className="secondary" href="/account/share">
        Invite someone to give with you
      </a>
    </Page>
  );
}
