import type { Metadata } from "next";
import Status from "./Status";

export const metadata: Metadata = { title: "Application status", robots: { index: false, follow: false } };

export default function ApplicationStatusPage() {
  return <Status />;
}
