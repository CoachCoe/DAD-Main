import type { Metadata } from "next";
import Finance from "./Finance";

export const metadata: Metadata = { title: "Refunds and cancellations" };

export default async function Page({ searchParams }: PageProps<"/staff/finance">) {
  const { donor, gift } = await searchParams;
  return <Finance donor={typeof donor === "string" ? donor : ""} gift={typeof gift === "string" ? gift : ""} />;
}
