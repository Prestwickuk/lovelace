import { NextResponse } from "next/server";
import { initiatePublicCheckout } from "@/lib/lovelace-api";
import { handleBffError } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

export async function POST(request: Request) {
  const rl = checkBffRateLimit(request, "write");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const body = (await request.json()) as {
      hold_id?: string;
      idempotency_key?: string;
      success_url?: string;
      cancel_url?: string;
    };
    if (body.success_url || body.cancel_url) {
      return NextResponse.json(
        {
          error: {
            code: "validation_failed",
            message: "Client-supplied checkout redirect URLs are prohibited.",
          },
        },
        { status: 422 },
      );
    }
    const res = await initiatePublicCheckout(
      {
        hold_id: body.hold_id || "",
        idempotency_key: body.idempotency_key,
      },
      rl.clientIp,
    );
    return NextResponse.json(res);
  } catch (err) {
    return handleBffError(err);
  }
}
