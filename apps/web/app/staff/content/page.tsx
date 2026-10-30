import type { Metadata } from "next";
import Content from "./Content";

export const metadata: Metadata = { title: "Source documents" };

export default function Page() {
  return <Content />;
}
