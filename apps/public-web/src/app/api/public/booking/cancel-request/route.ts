import { NextResponse } from "next/server";
import { requestBookingCancellation } from "@/lib/lovelace-api";
import { handleBffError } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

export async function POST(request: Request) {
  const rl = checkBffRateLimit(request, "lookup");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const body = await request.json();
    const res = await requestBookingCancellation(body, rl.clientIp);
    return NextResponse.json(res, {
      headers: {
        "Cache-Control": "no-store, no-cache, must-revalidate, private",
        "Referrer-Policy": "no-referrer",
      },
    });
  } catch (err) {
    return handleBffError(err);
  }
}
