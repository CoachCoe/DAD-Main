import type { Metadata } from "next";
import FailedPayments from "./FailedPayments";

export const metadata: Metadata = { title: "Failed payments" };

export default function Page() {
  return <FailedPayments />;
}
