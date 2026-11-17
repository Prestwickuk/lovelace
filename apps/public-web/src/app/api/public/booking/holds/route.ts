import { NextResponse } from "next/server";
import { createPublicHold } from "@/lib/lovelace-api";
import { handleBffError, parseJsonObject, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

export async function POST(request: Request) {
  const rl = checkBffRateLimit(request, "write");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const body = await parseJsonObject(request);
    const res = await createPublicHold(body, rl.clientIp);
    return NextResponse.json(res, { status: 201, headers: PRIVATE_NO_STORE_HEADERS });
  } catch (err) {
    return handleBffError(err);
  }
}
