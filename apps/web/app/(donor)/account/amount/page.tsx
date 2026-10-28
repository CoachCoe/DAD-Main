import type { Metadata } from "next";
import Amount from "./Amount";

export const metadata: Metadata = { title: "Change your daily gift" };

export default function Page() {
  return <Amount />;
}
