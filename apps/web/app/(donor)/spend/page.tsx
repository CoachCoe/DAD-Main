import type { Metadata } from "next";
import { Page } from "@/components/donor/Page";
import { ContentUnavailable, SpendByCategory } from "@/components/donor/PublicContent";
import { loadOverallSpend } from "@/lib/publicContent";

export const metadata: Metadata = {
  title: "Spend by category",
  description: "What DAD has spent across its published initiatives, shown as totals by category.",
};

export default async function SpendPage() {
  const spend = await loadOverallSpend();
  return (
    <Page wide eyebrow="Transparency" title="Spend by category">
      <p className="lead">
        Totals by category across DAD&apos;s published initiatives. DAD shows category totals only, not individual
        transactions. DAD launches in April 2027.
      </p>
      {spend ? <SpendByCategory spend={spend} /> : <ContentUnavailable />}
    </Page>
  );
}
