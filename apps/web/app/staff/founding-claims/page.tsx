import type { Metadata } from "next";
import FoundingClaims from "./FoundingClaims";

export const metadata: Metadata = { title: "Waitlist Founding Donors" };

export default function Page() {
  return <FoundingClaims />;
}
