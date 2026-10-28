import type { Metadata } from "next";
import Wording from "./Wording";

export const metadata: Metadata = { title: "Badge and milestone wording" };

export default function Page() {
  return <Wording />;
}
