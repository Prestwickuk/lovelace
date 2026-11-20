import { NextResponse } from "next/server";
import { sanitizePropertySlug } from "@/lib/branding";
import { fetchPublicCatalog } from "@/lib/lovelace-api";
import { handleBffError } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

export async function GET(request: Request) {
  const rl = checkBffRateLimit(request, "read");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const { searchParams } = new URL(request.url);
    const rawProp = searchParams.get("property") || "";
    const explicitSlug = rawProp.trim() ? sanitizePropertySlug(rawProp, "") : "";
    if (rawProp.trim() && !explicitSlug) {
      return NextResponse.json(
        { error: { code: "invalid_param", message: "Invalid property slug." } },
        { status: 400 },
      );
    }

    const catalog = await fetchPublicCatalog(explicitSlug || undefined);
    return NextResponse.json(catalog, {
      status: 200,
      headers: {
        "Cache-Control": "no-store, max-age=0",
      },
    });
  } catch (err) {
    return handleBffError(err);
  }
}
