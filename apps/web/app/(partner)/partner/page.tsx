import type { Metadata } from "next";
import Dashboard from "./Dashboard";

export const metadata: Metadata = { title: "Partner dashboard" };

export default function Page() {
  return <Dashboard />;
}
