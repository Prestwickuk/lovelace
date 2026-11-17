import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import {
  assertNoQuerySecrets,
  assertSafeBookingRef,
  callLovelacePortal,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalBookingDetail } from "@/lib/types";

export async function GET(
  req: NextRequest,
  context: { params: Promise<{ ref: string }> },
) {
  const limited = enforcePortalBffRateLimit(req, "portal-booking-detail", 60);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    const { ref } = await context.params;
    const cleanRef = assertSafeBookingRef(ref);
    const res = await callLovelacePortal<{ booking: PortalBookingDetail }>(
      req,
      `/bookings/${encodeURIComponent(cleanRef)}`,
      { method: "GET" },
      { requireSession: true, requireCsrf: false },
    );
    return NextResponse.json(res, {
      status: 200,
      headers: PRIVATE_NO_STORE_HEADERS,
    });
  } catch (err) {
    return handlePortalBffError(err);
  }
}
