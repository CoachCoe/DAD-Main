import type { Metadata } from "next";
import Amount from "./Amount";

export const metadata: Metadata = { title: "Change monthly amount" };

export default function Page() {
  return <Amount />;
}
