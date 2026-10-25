import type { Metadata } from "next";
import Employer from "./Employer";

export const metadata: Metadata = { title: "Employer and team" };

export default function Page() {
  return <Employer />;
}
