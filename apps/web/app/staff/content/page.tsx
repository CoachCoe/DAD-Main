import type { Metadata } from "next";
import Content from "./Content";

export const metadata: Metadata = { title: "Spreadsheet publication" };

export default function Page() {
  return <Content />;
}
