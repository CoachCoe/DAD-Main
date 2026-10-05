import type { Metadata } from "next";
import RecurringGiving from "./Recurring";

export const metadata: Metadata = { title: "My monthly giving" };

export default function Page() {
  return <RecurringGiving />;
}
