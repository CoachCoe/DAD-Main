import type { Metadata } from "next";
import EmailChange from "./EmailChange";

export const metadata: Metadata = { title: "Confirm your email change" };

export default function Page() {
  return <EmailChange />;
}
