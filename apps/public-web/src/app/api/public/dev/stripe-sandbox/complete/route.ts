import { NextResponse } from "next/server";
import { callLovelacePublic } from "@/lib/lovelace-api";
import { handleBffError } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

function isSandboxAllowed(): boolean {
  const flag = (process.env.LOVELACE_STRIPE_SANDBOX || "").trim().toLowerCase();
  if (process.env.NODE_ENV === "production") {
    return flag === "true";
  }
  return flag !== "false";
}

export async function POST(request: Request) {
  if (!isSandboxAllowed()) {
    return NextResponse.json(
      { error: { code: "not_found", message: "Endpoint unavailable" } },
      { status: 404 },
    );
  }

  const rl = checkBffRateLimit(request, "write");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const body = await request.json();
    const res = await callLovelacePublic(
      "/api/public/v1/dev/stripe-sandbox/complete",
      {
        method: "POST",
        body: JSON.stringify(body),
      },
      rl.clientIp,
    );
    return NextResponse.json(res);
  } catch (err) {
    return handleBffError(err);
  }
}
