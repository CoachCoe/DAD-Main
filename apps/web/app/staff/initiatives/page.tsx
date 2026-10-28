import type { Metadata } from "next";
import Initiatives from "./Initiatives";

export const metadata: Metadata = { title: "Initiatives" };

export default function Page() {
  return <Initiatives />;
}
