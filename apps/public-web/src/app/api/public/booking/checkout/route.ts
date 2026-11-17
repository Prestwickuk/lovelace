import { NextResponse } from "next/server";
import { initiatePublicCheckout } from "@/lib/lovelace-api";
import { handleBffError, parseJsonObject, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

export async function POST(request: Request) {
  const rl = checkBffRateLimit(request, "write");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const body = await parseJsonObject<{
      hold_id?: string;
      idempotency_key?: string;
      success_url?: string;
      cancel_url?: string;
    }>(request);
    if (body.success_url || body.cancel_url) {
      return NextResponse.json(
        {
          error: {
            code: "validation_failed",
            message: "Client-supplied checkout redirect URLs are prohibited.",
          },
        },
        { status: 422, headers: PRIVATE_NO_STORE_HEADERS },
      );
    }
    const res = await initiatePublicCheckout(
      {
        hold_id: body.hold_id || "",
        idempotency_key: body.idempotency_key,
      },
      rl.clientIp,
    );
    return NextResponse.json(res, { headers: PRIVATE_NO_STORE_HEADERS });
  } catch (err) {
    return handleBffError(err);
  }
}
