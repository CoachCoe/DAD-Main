import type { Metadata } from "next";
import Audit from "./Audit";

export const metadata: Metadata = { title: "Staff audit history" };

export default function Page() {
  return <Audit />;
}
