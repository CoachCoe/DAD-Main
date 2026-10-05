import type { Metadata } from "next";
import { IBM_Plex_Mono, Inter, Space_Grotesk } from "next/font/google";
import { SiteFrame } from "@/components/donor/SiteFrame";
import "./globals.css";
import { webTokenVariables } from "@/design/web";

// Inter and Space Grotesk are variable fonts, so they load their whole weight axis and
// must not be given a `weight`. IBM Plex Mono is static and has to name its weights.
const sans = Inter({ subsets: ["latin"], variable: "--font-sans-face", display: "swap" });

const display = Space_Grotesk({
  subsets: ["latin"],
  variable: "--font-display-face",
  display: "swap",
});

const mono = IBM_Plex_Mono({
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  variable: "--font-mono-face",
  display: "swap",
});

export const metadata: Metadata = {
  title: "Dynamic Aid Delivery — Join the founding donors",
  description: "Give, invite people you know, and see your circle’s impact. DAD launches in April 2027.",
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" style={webTokenVariables} className={`${sans.variable} ${display.variable} ${mono.variable}`}>
      <body>
        <SiteFrame>{children}</SiteFrame>
      </body>
    </html>
  );
}
