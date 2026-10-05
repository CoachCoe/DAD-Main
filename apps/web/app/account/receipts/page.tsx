import type { Metadata } from "next";
import { defaultSummaryYear, taxYear } from "@/lib/taxYear";
import Receipts from "./Receipts";

export const metadata: Metadata = { title: "Gift records and annual summaries" };

export default async function Page({ searchParams }: PageProps<"/account/receipts">) {
  const { gift } = await searchParams;
  const now = new Date();
  return <Receipts gift={typeof gift === "string" ? gift : undefined} latestYear={taxYear(now)} defaultYear={defaultSummaryYear(now)} />;
}
