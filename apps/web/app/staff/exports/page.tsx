import type { Metadata } from "next";
import Exports from "./Exports";

export const metadata: Metadata = { title: "Exports" };

export default function Page() {
  return <Exports />;
}
