import { permanentRedirect } from "next/navigation";

// The spec defines one "About / how it works" screen; /about stays valid for old links.
export default function About() {
  permanentRedirect("/how-it-works");
}
