import type { Metadata } from "next";
import { defaultSummaryYear, FIRST_SUMMARY_YEAR, taxYear } from "@/lib/taxYear";
import Receipts from "./Receipts";

export const metadata: Metadata = { title: "Gift records and annual summaries" };

export default async function Page({ searchParams }: PageProps<"/account/receipts">) {
  const { gift, year } = await searchParams;
  const now = new Date();
  const latestYear = taxYear(now);
  const chosen =
    typeof year === "string" && /^\d{4}$/.test(year) && Number(year) >= FIRST_SUMMARY_YEAR && Number(year) <= latestYear
      ? Number(year)
      : undefined;
  return (
    <Receipts
      gift={typeof gift === "string" ? gift : undefined}
      year={chosen}
      latestYear={latestYear}
      defaultYear={defaultSummaryYear(now)}
    />
  );
}
