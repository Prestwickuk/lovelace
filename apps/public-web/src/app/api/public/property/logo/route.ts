import { NextResponse } from "next/server";
import { sanitizePropertySlug } from "@/lib/branding";
import { fetchPublicPropertyLogo } from "@/lib/lovelace-api";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

const ALLOWED_LOGO_TYPES = new Set(["image/png", "image/jpeg", "image/webp"]);

export async function GET(request: Request) {
  const rl = checkBffRateLimit(request, "read");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  const { searchParams } = new URL(request.url);
  const slug = sanitizePropertySlug(searchParams.get("property") ?? undefined);

  try {
    const upstream = await fetchPublicPropertyLogo(slug, rl.clientIp);
    if (!upstream.ok || !upstream.body) {
      return new NextResponse(null, { status: 404 });
    }
    const contentType = (upstream.headers.get("content-type") || "")
      .split(";")[0]
      .trim()
      .toLowerCase();
    if (!ALLOWED_LOGO_TYPES.has(contentType)) {
      return new NextResponse(null, { status: 415 });
    }

    return new NextResponse(upstream.body, {
      status: 200,
      headers: {
        "Content-Type": contentType,
        "X-Content-Type-Options": "nosniff",
        "Cache-Control": "public, max-age=300",
      },
    });
  } catch {
    return new NextResponse(null, { status: 404 });
  }
}
