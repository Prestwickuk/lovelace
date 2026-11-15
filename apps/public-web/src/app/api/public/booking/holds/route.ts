import { NextResponse } from "next/server";
import { createPublicHold } from "@/lib/lovelace-api";
import { handleBffError } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

export async function POST(request: Request) {
  const rl = checkBffRateLimit(request, "write");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const body = await request.json();
    const res = await createPublicHold(body, rl.clientIp);
    return NextResponse.json(res, { status: 201 });
  } catch (err) {
    return handleBffError(err);
  }
}
