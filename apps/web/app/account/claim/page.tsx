import type { Metadata } from "next";
import Claim from "./Claim";

export const metadata: Metadata = { title: "Claim your account" };

export default function Page() {
  return <Claim />;
}
