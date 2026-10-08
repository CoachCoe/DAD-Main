import type { Metadata } from "next";
import { connection } from "next/server";
import { StaffFrame } from "@/components/donor/StaffFrame";

export const metadata: Metadata = { robots: { index: false, follow: false } };

export default async function StaffLayout({ children }: LayoutProps<"/staff">) {
  // proxy.ts issues a nonce per request, and a prerendered page could not carry it.
  await connection();
  return <StaffFrame>{children}</StaffFrame>;
}
