import { NextResponse } from "next/server";
import { fetchBookingConfirmation } from "@/lib/lovelace-api";
import { handleBffError } from "@/lib/bff-response";
import { checkBffRateLimit, rateLimitResponse } from "@/lib/rate-limit";

const CONFIRMATION_HEADERS = {
  "Cache-Control": "no-store, no-cache, must-revalidate, private",
  Pragma: "no-cache",
  Expires: "0",
  "Referrer-Policy": "no-referrer",
  "X-Robots-Tag": "noindex, nofollow",
};

export async function POST(request: Request) {
  const rl = checkBffRateLimit(request, "lookup");
  if (!rl.allowed) return rateLimitResponse(rl.retryAfterSeconds);

  try {
    const body = (await request.json()) as {
      booking_reference?: string;
      token?: string;
    };
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
        { status: 400, headers: CONFIRMATION_HEADERS },
      );
    }

    const res = await fetchBookingConfirmation(ref, token, rl.clientIp);
    return NextResponse.json(res, { headers: CONFIRMATION_HEADERS });
  } catch (err) {
    const res = handleBffError(err);
    Object.entries(CONFIRMATION_HEADERS).forEach(([k, v]) =>
      res.headers.set(k, v),
    );
    return res;
  }
}
