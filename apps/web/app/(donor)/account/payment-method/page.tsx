import type { Metadata } from "next";
import PaymentMethod from "./PaymentMethod";

export const metadata: Metadata = { title: "Update payment method" };

export default function Page() {
  return <PaymentMethod />;
}
