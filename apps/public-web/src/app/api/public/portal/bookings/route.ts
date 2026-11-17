import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import {
  assertNoQuerySecrets,
  callLovelacePortal,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalBookingSummary } from "@/lib/types";

export async function GET(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-bookings-list", 60);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    const res = await callLovelacePortal<{ bookings: PortalBookingSummary[] }>(
      req,
      "/bookings",
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
