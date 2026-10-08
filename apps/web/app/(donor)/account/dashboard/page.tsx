import type { Metadata } from "next";
import Dashboard from "./Dashboard";

export const metadata: Metadata = { title: "Your dashboard" };

export default function DashboardPage() {
  return <Dashboard />;
}
