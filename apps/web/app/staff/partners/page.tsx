import type { Metadata } from "next";
import Inquiries from "./Inquiries";

export const metadata: Metadata = { title: "Partner inquiries" };

export default function Page() {
  return <Inquiries />;
}
