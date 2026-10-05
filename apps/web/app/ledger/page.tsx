import type { Metadata } from "next";
import { ContentUnavailable, Ledger } from "@/components/donor/PublicContent";
import { loadPublicContent } from "@/lib/publicContent";

export const metadata: Metadata = {
  title: "Impact ledger",
  description: "DAD’s staff-updated impact ledger: dated figures, each traceable to a source document, with illustrative figures labeled.",
};

export default async function LedgerPage() {
  const content = await loadPublicContent();
  return <section className="panel">
    <span className="eyebrow">DAD · IMPACT LEDGER</span>
    <h1>Impact ledger</h1>
    <p>A staff-updated record, with a stated cadence and a source document behind every reported figure. DAD launches in April 2027.</p>
    {content ? <Ledger content={content} /> : <ContentUnavailable />}
  </section>;
}
