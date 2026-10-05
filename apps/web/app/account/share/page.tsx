import type { Metadata } from "next";
import Share from "./Share";

export const metadata: Metadata = { title: "Share and invite" };

export default function Page() {
  return <Share />;
}
