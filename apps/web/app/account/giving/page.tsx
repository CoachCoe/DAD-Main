import type { Metadata } from "next";
import Giving from "./Giving";

export const metadata: Metadata = { title: "My giving" };

export default function Page() {
  return <Giving />;
}
