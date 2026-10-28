import type { Metadata } from "next";
import Donate from "./Donate";

export const metadata: Metadata = {
  title: "Give",
  description: "Give about a dollar a day — $30/month. DAD launches in April 2027.",
};

export default function DonatePage() {
  return <Donate />;
}
