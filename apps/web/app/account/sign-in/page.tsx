import type { Metadata } from "next";
import SignIn from "./SignIn";

export const metadata: Metadata = { title: "Claim your account or sign in" };

export default function Page() {
  return <SignIn />;
}
