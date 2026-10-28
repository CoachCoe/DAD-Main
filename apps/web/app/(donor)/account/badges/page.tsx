import type { Metadata } from "next";
import Badges from "./Badges";

export const metadata: Metadata = { title: "Badges" };

export default function Page() {
  return <Badges />;
}
