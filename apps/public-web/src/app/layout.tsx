import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Tir Bach Campsite | Pembrokeshire Coastal Glamping & Pitches",
    template: "%s | Tir Bach Campsite",
  },
  description:
    "Wildflower meadow camping, furnished bell tents, handcrafted Preseli shepherd's huts, and coastal touring pitches in Pembrokeshire, Wales.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
