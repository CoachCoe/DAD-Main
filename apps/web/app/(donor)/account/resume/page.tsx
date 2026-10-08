import type { Metadata } from "next";
import Resume from "./Resume";

export const metadata: Metadata = { title: "Resume monthly giving" };

export default function Page() {
  return <Resume />;
}
