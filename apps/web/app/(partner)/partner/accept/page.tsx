import type { Metadata } from "next";
import Accept from "./Accept";

export const metadata: Metadata = { title: "Partner sign-in" };

export default function Page() {
  return <Accept />;
}
