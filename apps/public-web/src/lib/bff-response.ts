import "server-only";

import { NextResponse } from "next/server";
import { LovelaceApiError } from "./lovelace-api";

export const PRIVATE_NO_STORE_HEADERS: Record<string, string> = {
  "Cache-Control": "no-store, no-cache, must-revalidate, private, max-age=0",
  Pragma: "no-cache",
  Expires: "0",
  "Referrer-Policy": "no-referrer",
  "X-Content-Type-Options": "nosniff",
  "X-Robots-Tag": "noindex, nofollow",
};

export async function parseJsonObject<
  T extends Record<string, unknown> = Record<string, unknown>,
>(req: Request): Promise<T> {
  let raw: unknown;
  try {
    raw = await req.json();
  } catch {
    throw new LovelaceApiError(400, "invalid_json", "Invalid JSON request body.");
  }
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    throw new LovelaceApiError(
      400,
      "invalid_json",
      "Request body must be a JSON object.",
    );
  }
  return raw as T;
}

export function handleBffError(
  err: unknown,
  extraHeaders: Record<string, string> = PRIVATE_NO_STORE_HEADERS,
) {
  if (err instanceof LovelaceApiError) {
    return NextResponse.json(
      {
        error: {
          code: err.code,
          message: err.message,
          fields: err.fields,
        },
      },
      { status: err.status, headers: extraHeaders },
    );
  }
  if (err instanceof SyntaxError) {
    return NextResponse.json(
      {
        error: {
          code: "invalid_json",
          message: "Invalid JSON request body.",
        },
      },
      { status: 400, headers: extraHeaders },
    );
  }
  return NextResponse.json(
    {
      error: {
        code: "upstream_unavailable",
        message: "Booking service temporarily unavailable. Please try again.",
      },
    },
    { status: 502, headers: extraHeaders },
  );
}

export function handlePortalBffError(err: unknown) {
  return handleBffError(err, PRIVATE_NO_STORE_HEADERS);
}
