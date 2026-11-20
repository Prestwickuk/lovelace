import type { NextConfig } from "next";

const isDev = process.env.NODE_ENV !== "production";
const isHttpsProd =
  (process.env.LOVELACE_ENV || "").trim().toLowerCase() === "production" ||
  (process.env.NEXT_PUBLIC_SITE_URL || "").trim().toLowerCase().startsWith("https://");

function buildCsp(frameAncestors: string): string {
  return [
    "default-src 'self'",
    isDev
      ? "script-src 'self' 'unsafe-inline' 'unsafe-eval'"
      : "script-src 'self' 'unsafe-inline'",
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: https:",
    "font-src 'self' data:",
    isDev ? "connect-src 'self' ws: wss:" : "connect-src 'self'",
    "object-src 'none'",
    `frame-ancestors ${frameAncestors}`,
    "base-uri 'self'",
    "form-action 'self'",
  ].join("; ");
}

const contentSecurityPolicy = buildCsp("'none'");
const embedContentSecurityPolicy = buildCsp(isHttpsProd ? "https:" : "https: http:");

const globalSecurityHeaders = [
  { key: "Content-Security-Policy", value: contentSecurityPolicy },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  {
    key: "Permissions-Policy",
    value: "geolocation=(), microphone=(), camera=()",
  },
  ...(isHttpsProd
    ? [{ key: "Strict-Transport-Security", value: "max-age=31536000; includeSubDomains" }]
    : []),
];

const embedSecurityHeaders = [
  { key: "Content-Security-Policy", value: embedContentSecurityPolicy },
  { key: "X-Content-Type-Options", value: "nosniff" },
  {
    key: "Permissions-Policy",
    value: "geolocation=(), microphone=(), camera=()",
  },
  ...(isHttpsProd
    ? [{ key: "Strict-Transport-Security", value: "max-age=31536000; includeSubDomains" }]
    : []),
  { key: "Cache-Control", value: "no-store, no-cache, must-revalidate, private" },
  { key: "Pragma", value: "no-cache" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  { key: "X-Robots-Tag", value: "noindex, nofollow" },
];

const privateNoStoreHeaders = [
  { key: "Cache-Control", value: "no-store, no-cache, must-revalidate, private" },
  { key: "Pragma", value: "no-cache" },
  { key: "Referrer-Policy", value: "no-referrer" },
  { key: "X-Robots-Tag", value: "noindex, nofollow" },
];

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  images: {
    unoptimized: true,
  },
  async headers() {
    return [
      {
        source: "/((?!embed(?:/|$)).*)",
        headers: globalSecurityHeaders,
      },
      {
        source: "/embed",
        headers: embedSecurityHeaders,
      },
      {
        source: "/embed/:path*",
        headers: embedSecurityHeaders,
      },
      {
        source: "/booking/confirmation",
        headers: privateNoStoreHeaders,
      },
      {
        source: "/lookup",
        headers: privateNoStoreHeaders,
      },
      {
        source: "/book",
        headers: privateNoStoreHeaders,
      },
      {
        source: "/checkout/sandbox",
        headers: privateNoStoreHeaders,
      },
      {
        source: "/portal",
        headers: privateNoStoreHeaders,
      },
      {
        source: "/portal/:path*",
        headers: privateNoStoreHeaders,
      },
      {
        source: "/api/public/portal/:path*",
        headers: privateNoStoreHeaders,
      },
      {
        source: "/api/public/booking/:path*",
        headers: privateNoStoreHeaders,
      },
    ];
  },
};

export default nextConfig;
