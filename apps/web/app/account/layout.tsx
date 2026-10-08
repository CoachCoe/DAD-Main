import type { Metadata } from "next";
import { headers } from "next/headers";
import { notFound } from "next/navigation";
import { connection } from "next/server";
import { UNSERVED_HEADER } from "@/lib/hosts";

export const metadata: Metadata = { robots: { index: false, follow: false } };

export default async function AccountLayout({ children }: LayoutProps<"/account">) {
  // proxy.ts issues a nonce per request, and a prerendered page could not carry it.
  await connection();
  if ((await headers()).has(UNSERVED_HEADER)) notFound();
  return children;
}
