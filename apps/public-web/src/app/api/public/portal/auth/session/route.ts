import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { LovelaceApiError } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  callLovelacePortal,
  clearPortalSessionCookie,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalSessionView } from "@/lib/types";

export async function GET(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-session-read", 90);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    const upstream = await callLovelacePortal<{ session: PortalSessionView }>(
      req,
      "/auth/session",
      { method: "GET" },
      { requireSession: true, requireCsrf: false },
    );
    return NextResponse.json(upstream, {
      status: 200,
      headers: PRIVATE_NO_STORE_HEADERS,
    });
  } catch (err) {
    const res = handlePortalBffError(err);
    if (err instanceof LovelaceApiError && err.status === 401) {
      clearPortalSessionCookie(res);
    }
    return res;
  }
}
