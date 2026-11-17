import { NextResponse } from "next/server";
import { rotateBookingConfirmationToken } from "@/lib/lovelace-api";
import { handleBffError, parseJsonObject, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

export async function POST(request: Request) {
  const rl = checkBffRateLimit(request, "lookup");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const body = await parseJsonObject<{
      booking_reference?: string;
      token?: string;
    }>(request);
    const res = await rotateBookingConfirmationToken(
      (body.booking_reference || "").trim(),
      (body.token || "").trim(),
      rl.clientIp,
    );
    return NextResponse.json(res, { headers: PRIVATE_NO_STORE_HEADERS });
  } catch (err) {
    return handleBffError(err, PRIVATE_NO_STORE_HEADERS);
  }
}
