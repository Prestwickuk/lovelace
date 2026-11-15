import { NextResponse } from "next/server";
import { computePublicPricing } from "@/lib/lovelace-api";
import { handleBffError } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

export async function POST(request: Request) {
  const rl = checkBffRateLimit(request, "read");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const body = await request.json();
    const res = await computePublicPricing(body, rl.clientIp);
    return NextResponse.json(res);
  } catch (err) {
    return handleBffError(err);
  }
}
