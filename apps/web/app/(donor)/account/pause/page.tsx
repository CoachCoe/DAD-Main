import type { Metadata } from "next";
import PauseGiving from "./Pause";

export const metadata: Metadata = { title: "Pause monthly giving" };

export default function Page() {
  return <PauseGiving />;
}
