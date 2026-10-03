import Link from "next/link";

export default function ThankYouPage() {
  return (
    <section className="panel authPanel">
      <span className="eyebrow">DAD · TEST CHECKOUT</span>
      <h1>You’re back from checkout</h1>
      <p>Payment confirmation is processed separately. Returning to this page does not confirm a successful gift.</p>
      <p className="note">This sandbox preview does not send donation receipts or award Founding Donor badges.</p>
      <p><Link href="/account/sign-in">Claim your donor account</Link> to view confirmed test gifts.</p>
      <Link className="button" href="/donate">Return to giving</Link>
    </section>
  );
}
