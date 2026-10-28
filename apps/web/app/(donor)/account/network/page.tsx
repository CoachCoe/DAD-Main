import type { Metadata } from "next";
import Network from "./Network";

export const metadata: Metadata = { title: "Your network" };

export default function Page() {
  return <Network />;
}
