import type { Metadata } from "next";
import { connection } from "next/server";

export const metadata: Metadata = { robots: { index: false, follow: false } };

export default async function AccountLayout({ children }: LayoutProps<"/account">) {
  // proxy.ts issues a nonce per request, and a prerendered page could not carry it.
  await connection();
  return children;
}
