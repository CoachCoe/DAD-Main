import Link from "next/link";

export const metadata = {
  title: "About Dynamic Aid Delivery",
  description: "Give, invite people you know, and help DAD prepare for its April 2027 launch.",
};

export default function About() {
  return <section className="panel">
    <span className="eyebrow">DYNAMIC AID DELIVERY</span>
    <h1>A small gift. A growing circle.</h1>
    <p>DAD brings donors and partner organizations together to support practical aid work. Each initiative is assembled with a partner and approved by the board. Programs invite people to enrol in that work.</p>
    <h2>Join before launch</h2>
    <p>DAD launches on 22 April 2027. A first gift completed before launch earns a permanent Founding Donor badge. Recurring giving starts at about a dollar a day, billed as $30/month. One-time gifts are welcome too.</p>
    <h2>Invite people you know</h2>
    <p>Give without creating an account, then claim your account to share your personal link or show your referral code on your phone. Your dashboard counts Link and IRL Referrals separately after a first gift completes.</p>
    <p>Your circle includes only people you personally refer. Attributed dollars reflect money actually collected, less successful refunds. Successful monthly gifts build your streak; one-time gifts do not.</p>
    <h2>Clear records, honest limits</h2>
    <p>The impact ledger is updated by staff on a stated cadence. Figures carry an as-of date, illustrative figures are labeled, and source documents support reported numbers.</p>
    <p>This donor release does not enrol recipients or send money to them. Aid programs begin at launch, with recipients and partners using separate web experiences.</p>
    <p>This environment is a sandbox preview using Stripe test payments. No real donation is collected.</p>
    <div className="uploadActions"><Link className="button" href="/donate">Try giving</Link><Link className="secondary" href="/ledger">Read the impact ledger</Link></div>
    <p><Link href="/how-it-works">How giving and referrals work</Link> · <Link href="/partners">Partner interest form</Link></p>
  </section>;
}
