import Link from "next/link";
export default function HowItWorks() {
  return <section className="panel"><h1>How Dynamic Aid Delivery works</h1>
    <p>Donors support DAD. DAD assembles initiatives with partner organizations and board approval. Programs invite recipients to enrol in that work.</p>
    <h2>Giving and your circle</h2><p>Choose recurring or one-time giving on the web. About $1 a day is billed as $30/month; the monthly amount is what your card is charged.</p>
    <p>After giving, claim your account to share a personal referral link or show your on-screen code. A Link Referral begins with a shared link; an IRL Referral begins with the marked code link. Credit is direct only and starts with a completed first gift.</p>
    <h2>Streaks and recognition</h2><p>Successful recurring months build a streak. One-time gifts contribute money but do not build a streak. Failed payments have a recovery grace period.</p>
    <p>A first gift before launch earns permanent Founding Donor recognition. DAD launches on 22 April 2027.</p>
    <h2>Programs start at launch</h2><p>This donor release does not enrol recipients or send money to them. Partners can register interest with the website form. Recipient and partner accounts arrive with R2.</p>
    <p>This is a sandbox preview. <Link href="/donate">Try giving</Link> · <Link href="/partners">Partner interest form</Link></p>
  </section>;
}
