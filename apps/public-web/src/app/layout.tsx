import type { Metadata } from "next";
import { buildPropertyBrandCssVars } from "@/lib/branding";
import { fetchPublicPropertyBranding } from "@/lib/lovelace-api";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Lovelace Direct Booking | Hospitality Stays, Cabins & Pitches",
    template: "%s | Direct Bookings",
  },
  description:
    "Direct accommodation search, live seasonal availability, handcrafted cabins, glamping stays, and touring pitches.",
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
