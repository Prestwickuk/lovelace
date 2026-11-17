import { NextResponse } from "next/server";
import { callLovelacePublic, isStripeSandboxAllowed } from "@/lib/lovelace-api";
import { handleBffError, parseJsonObject, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

export async function POST(request: Request) {
  if (!isStripeSandboxAllowed()) {
    return NextResponse.json(
      { error: { code: "not_found", message: "Endpoint unavailable" } },
      { status: 404, headers: PRIVATE_NO_STORE_HEADERS },
    );
  }

  const rl = checkBffRateLimit(request, "write");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const body = await parseJsonObject(request);
    const res = await callLovelacePublic(
      "/api/public/v1/dev/stripe-sandbox/complete",
      {
        method: "POST",
        body: JSON.stringify(body),
      },
      rl.clientIp,
    );
    return NextResponse.json(res, { headers: PRIVATE_NO_STORE_HEADERS });
  } catch (err) {
    return handleBffError(err);
  }
}
