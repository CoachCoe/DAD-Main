import Link from "next/link";
export default function DonorHome() {
  return <>
    <section className="hero">
      <div className="heroCopy"><span className="eyebrow">DYNAMIC AID DELIVERY · DONOR PREVIEW</span>
        <h1>Start small. Invite someone. Grow your circle’s impact.</h1>
        <p>Join with about a dollar a day · $30/month. Recurring giving is the default, and one-time gifts are welcome.</p>
        <p>DAD launches on 22 April 2027. Give before launch to earn a permanent Founding Donor badge.</p>
        <p>This environment uses Stripe test payments. No real donation is collected.</p>
        <div className="uploadActions"><Link className="button" href="/donate">Try giving</Link><Link className="secondary" href="/how-it-works">How DAD works</Link></div>
      </div>
      <aside className="heroFacts" aria-label="DAD at a glance"><dl>
        <div><dt>Monthly gift</dt><dd><span>$30/month</span><span>about $1 a day</span></dd></div>
        <div><dt>Recognition</dt><dd>Founding Donor<small>Permanent, for a first gift before launch</small></dd></div>
        <div><dt>Launch</dt><dd>22 April 2027<small>Aid programs begin</small></dd></div>
      </dl></aside>
    </section>
    <section className="panel"><h2>One generous invitation at a time</h2>
      <ol><li>Give on the web, without creating an account first.</li><li>Claim your account from the confirmation email.</li><li>Share your personal link or show your referral code on your phone.</li><li>See Link and IRL Referrals, collected dollars, your monthly streak, and your badge.</li></ol>
      <p>Only the people you personally invite count toward your circle. A referral counts when their first gift completes.</p>
    </section>
    <section className="panel"><h2>Building toward April</h2><p>DAD assembles aid initiatives with partner organizations and board approval. Programs begin at launch. No recipient receives money through this donor release.</p>
      <p><Link href="/initiatives">Explore planned initiatives</Link> · <Link href="/ledger">Read the impact ledger</Link> · <Link href="/partners">Become a partner</Link></p>
    </section>
  </>;
}
