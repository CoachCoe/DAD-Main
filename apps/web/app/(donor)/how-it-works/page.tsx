import type { Metadata } from "next";
import Link from "next/link";
import { Page } from "@/components/donor/Page";

export const metadata: Metadata = {
  title: "About DAD and how it works",
  description:
    "How giving, referrals, streaks, and the Founding Donor badge work, and what DAD’s April 2027 launch brings.",
};

export default function HowItWorks() {
  return (
    <Page eyebrow="How it works" title="A small gift. A growing circle.">
      <p className="lead">
        DAD brings donors and partner organizations together to support practical aid work. Each initiative is assembled
        with a partner and approved by the board. From launch, programs invite people to enrol in that work.
      </p>
      <h2>Giving</h2>
      <p>
        Give monthly on the web, without creating an account first. Giving suggests about a dollar a day, billed as
        $30/month, and $90, $150, or an amount you choose up to $900/month work too: the monthly amount is what your
        card is charged.
      </p>
      <h2>Your circle</h2>
      <p>
        After giving, claim your account to share your personal link or show your referral code on your phone. A Link
        Referral begins with a shared link; an IRL Referral begins when someone scans your code. Your dashboard counts
        them separately, once a first gift completes.
      </p>
      <p>
        Your circle includes only people you personally refer. Attributed dollars reflect money actually collected, less
        successful refunds.
      </p>
      <h2>Streaks and recognition</h2>
      <p>
        Successful monthly gifts keep your streak growing in local calendar days. A failed renewal has 7 days to recover
        before your active streak resets. Cancellation ends it at the first skipped renewal. Your historical best stays
        recorded.
      </p>
      <p>
        DAD launches on 22 April 2027. A first gift completed before 1 January 2027 (New York time) earns a permanent
        Founding Donor badge.
      </p>
      <h2>Clear records, honest limits</h2>
      <p>
        DAD publishes spend as totals by category, never as individual transactions. Staff verify each figure against
        finance records before it is published.
      </p>
      <h2>Programs start at launch</h2>
      <p>
        This donor release does not enrol recipients or send money to them. Aid programs begin at launch. Partners can
        apply now; approved partners use the partner portal to manage their profile, initiatives, employee giving, and
        reports. Donor apps for iOS and Android are being developed separately.
      </p>
      <p className="note">
        This environment is a sandbox preview using Stripe test payments. No real donation is collected.
      </p>
      <div className="actions">
        <Link className="button" href="/donate">
          Try giving
        </Link>
        <Link className="secondary" href="/spend">
          See spend by category
        </Link>
      </div>
      <p>
        <Link href="/apply">Partner application</Link>
        {" · "}
        <a href="/partner/sign-in">Partner sign-in</a>
      </p>
    </Page>
  );
}
