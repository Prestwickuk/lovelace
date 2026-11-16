import type { Metadata } from "next";
import { buildPropertyBrandCssVars } from "@/lib/branding";
import { fetchPublicPropertyBranding } from "@/lib/lovelace-api";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Tir Bach Campsite | Pembrokeshire Coastal Glamping & Pitches",
    template: "%s | Tir Bach Campsite",
  },
  description:
    "Wildflower meadow camping, furnished bell tents, handcrafted Preseli shepherd's huts, and coastal touring pitches in Pembrokeshire, Wales.",
};

export default async function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  const branding = await fetchPublicPropertyBranding();
  const brandVars = buildPropertyBrandCssVars(branding);
  return (
    <html lang="en">
      <body style={brandVars}>{children}</body>
    </html>
  );
}
