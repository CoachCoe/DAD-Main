import type { Metadata } from "next";
import PartnerDetail from "./PartnerDetail";

export const metadata: Metadata = { title: "Partner" };

export default async function Page({ params }: PageProps<"/staff/partners/[partnerId]">) {
  const { partnerId } = await params;
  return <PartnerDetail partner={partnerId} />;
}
