import { NextResponse } from "next/server";
import { computePublicPricing } from "@/lib/lovelace-api";
import { handleBffError, parseJsonObject } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

export async function POST(request: Request) {
  const rl = checkBffRateLimit(request, "read");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const body = await parseJsonObject(request);
    const res = await computePublicPricing(
      body as Parameters<typeof computePublicPricing>[0],
      rl.clientIp,
    );
    return NextResponse.json(res);
  } catch (err) {
    return handleBffError(err);
  }
}
