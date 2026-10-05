import type { Metadata } from "next";
import DeliveryHistory from "./Messages";

export const metadata: Metadata = { title: "Donor delivery history" };

export default async function Page({ searchParams }: PageProps<"/staff/messages">) {
  const { donor } = await searchParams;
  return <DeliveryHistory donor={typeof donor === "string" ? donor : ""} />;
}
