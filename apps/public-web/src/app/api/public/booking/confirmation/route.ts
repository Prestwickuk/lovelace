import { NextResponse } from "next/server";
import { fetchBookingConfirmation } from "@/lib/lovelace-api";
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
    const ref = (body.booking_reference || "").trim();
    const authHeader = request.headers.get("authorization") || "";
    const bearerToken = authHeader.toLowerCase().startsWith("bearer ")
      ? authHeader.slice(7).trim()
      : "";
    const token = bearerToken || (body.token || "").trim();

    if (!ref || !token) {
      return NextResponse.json(
        {
          error: {
            code: "missing_credentials",
            message: "Booking reference and access token are required.",
          },
        },
        { status: 400, headers: PRIVATE_NO_STORE_HEADERS },
      );
    }

    const res = await fetchBookingConfirmation(ref, token, rl.clientIp);
    return NextResponse.json(res, { headers: PRIVATE_NO_STORE_HEADERS });
  } catch (err) {
    return handleBffError(err, PRIVATE_NO_STORE_HEADERS);
  }
}
