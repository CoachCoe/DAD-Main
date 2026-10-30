import type { Metadata } from "next";
import Deletions from "./Deletions";

export const metadata: Metadata = { title: "Account deletions" };

export default function Page() {
  return <Deletions />;
}
