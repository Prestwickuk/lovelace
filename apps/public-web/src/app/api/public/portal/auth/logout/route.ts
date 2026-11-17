import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug, LovelaceApiError } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSameOriginMutation,
  callLovelacePortal,
  clearPortalSessionCookie,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";

export async function POST(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-logout", 30);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    await callLovelacePortal(
      req,
      "/auth/logout",
      {
        method: "POST",
        body: JSON.stringify({ property_slug: getPropertySlug() }),
      },
      { requireSession: true, requireCsrf: true },
    );
    const res = NextResponse.json(
      { logged_out: true },
      { status: 200, headers: PRIVATE_NO_STORE_HEADERS },
    );
    clearPortalSessionCookie(res);
    return res;
  } catch (err) {
    if (err instanceof LovelaceApiError && err.status === 401) {
      const res = NextResponse.json(
        { logged_out: true },
        { status: 200, headers: PRIVATE_NO_STORE_HEADERS },
      );
      clearPortalSessionCookie(res);
      return res;
    }
    return handlePortalBffError(err);
  }
}
