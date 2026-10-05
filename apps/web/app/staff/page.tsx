import type { Metadata } from "next";
import Staff from "./Staff";

export const metadata: Metadata = { title: "Staff back office" };

export default async function Page({ searchParams }: PageProps<"/staff">) {
  const { q } = await searchParams;
  return <Staff q={typeof q === "string" ? q : null} />;
}
