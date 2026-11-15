import { NextResponse } from "next/server";
import { cancelPublicHold } from "@/lib/lovelace-api";
import { handleBffError } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

export async function POST(
  request: Request,
  context: { params: Promise<{ id: string }> },
) {
  const rl = checkBffRateLimit(request, "write");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const { id } = await context.params;
    const res = await cancelPublicHold(id, rl.clientIp);
    return NextResponse.json(res);
  } catch (err) {
    return handleBffError(err);
  }
}
