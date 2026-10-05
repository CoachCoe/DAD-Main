import type { Metadata } from "next";
import Staff from "./Staff";

export const metadata: Metadata = { title: "Staff back office" };

export default function Page() {
  return <Staff />;
}
