import type { Metadata } from "next";

export const metadata: Metadata = { title: "Terms, privacy, and donor consent" };

export default function LegalPage() {
  return <section className="panel"><h1>Terms, privacy, and donor consent</h1>
    <p><strong>Sandbox preview. Approved legal documents have not been published here.</strong></p>
    <p>Use test information and Stripe test cards. This environment does not collect live donations or issue legal tax receipts.</p>
    <h2>Before live donations</h2><p>DAD must publish its approved Terms of Service, Privacy Policy, donor consent language, verified receiving-entity details, and receipt wording before live giving is enabled.</p>
    <p>Optional referral and marketing notifications are separate choices in the donor account. A public nickname is shared only after explicit sharing activation.</p>
    <p>DAD launches in April 2027. This page is a statement of the preview’s status, not a substitute for approved legal documents.</p>
  </section>;
}
