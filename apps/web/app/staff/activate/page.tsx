import type { Metadata } from "next";
import Activate from "./Activate";

export const metadata: Metadata = { title: "Activate staff account" };

export default function Page() {
  return <Activate />;
}
