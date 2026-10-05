import type { Metadata } from "next";
import Code from "./Code";

export const metadata: Metadata = { title: "My referral code" };

export default function Page() {
  return <Code />;
}
