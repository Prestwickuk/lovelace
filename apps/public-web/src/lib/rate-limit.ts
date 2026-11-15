import { NextResponse } from "next/server";

export interface RateLimitWindow {
  count: number;
  resetAtMs: number;
}

export class SlidingFixedWindowLimiter {
  private buckets = new Map<string, RateLimitWindow>();
  private readonly limit: number;
  private readonly windowMs: number;

  constructor(limit: number, windowMs: number) {
    this.limit = limit;
    this.windowMs = windowMs;
  }

  allow(key: string, nowMs = Date.now()): { allowed: boolean; retryAfterSeconds: number } {
    if (this.buckets.size > 5000) {
      for (const [k, v] of this.buckets.entries()) {
        if (nowMs >= v.resetAtMs) {
          this.buckets.delete(k);
        }
      }
    }
    const cur = this.buckets.get(key);
    if (!cur || nowMs >= cur.resetAtMs) {
      this.buckets.set(key, { count: 1, resetAtMs: nowMs + this.windowMs });
      return { allowed: true, retryAfterSeconds: 0 };
    }
    if (cur.count >= this.limit) {
      const retryAfterSeconds = Math.max(1, Math.ceil((cur.resetAtMs - nowMs) / 1000));
      return { allowed: false, retryAfterSeconds };
    }
    cur.count += 1;
    return { allowed: true, retryAfterSeconds: 0 };
  }

  reset() {
    this.buckets.clear();
  }
}

function normalizeIp(raw: string): string {
  const trimmed = raw.trim();
  if (!trimmed) return "";
  // Strip IPv6 brackets and optional :port on IPv4
  if (trimmed.startsWith("[")) {
    const end = trimmed.indexOf("]");
    if (end > 1) return trimmed.slice(1, end);
  }
  const zoneIdx = trimmed.indexOf("%");
  const withoutZone = zoneIdx >= 0 ? trimmed.slice(0, zoneIdx) : trimmed;
  // IPv4 with port e.g. 1.2.3.4:5678
  const dotParts = withoutZone.split(".");
  if (dotParts.length === 4 && withoutZone.includes(":")) {
    return withoutZone.split(":")[0];
  }
  if (withoutZone.startsWith("::ffff:")) {
    return withoutZone.slice(7);
  }
  return withoutZone;
}

function ipv4ToUint32(ip: string): number | null {
  const parts = ip.split(".");
  if (parts.length !== 4) return null;
  let acc = 0;
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return null;
    const n = Number(part);
    if (n < 0 || n > 255) return null;
    acc = (acc << 8) | n;
  }
  return acc >>> 0;
}

function ipv6ToBigInt(ip: string): bigint | null {
  if (!ip.includes(":")) return null;
  const halves = ip.split("::");
  if (halves.length > 2) return null;
  const parseGroup = (s: string) =>
    s ? s.split(":").filter((g) => g.length > 0) : [];
  const left = parseGroup(halves[0]);
  const right = halves.length === 2 ? parseGroup(halves[1]) : [];
  const totalGroups = left.length + right.length;
  if (halves.length === 1 && totalGroups !== 8) return null;
  if (halves.length === 2 && totalGroups > 8) return null;
  const zeros = halves.length === 2 ? Array(8 - totalGroups).fill("0") : [];
  const full = [...left, ...zeros, ...right];
  if (full.length !== 8) return null;
  let val = 0n;
  for (const hex of full) {
    if (!/^[0-9a-fA-F]{1,4}$/.test(hex)) return null;
    val = (val << 16n) | BigInt(parseInt(hex, 16));
  }
  return val;
}

export function isValidIp(raw: string): boolean {
  const ip = normalizeIp(raw);
  return ipv4ToUint32(ip) !== null || ipv6ToBigInt(ip) !== null;
}

export function isIpInCidr(rawIp: string, rawCidr: string): boolean {
  const ip = normalizeIp(rawIp);
  const cidr = rawCidr.trim();
  if (!ip || !cidr) return false;
  const [netRaw, prefixRaw] = cidr.includes("/")
    ? cidr.split("/", 2)
    : [cidr, ""];
  const netIp = normalizeIp(netRaw);

  const ip4 = ipv4ToUint32(ip);
  const net4 = ipv4ToUint32(netIp);
  if (ip4 !== null && net4 !== null) {
    const bits = prefixRaw === "" ? 32 : Number(prefixRaw);
    if (!Number.isInteger(bits) || bits < 0 || bits > 32) return false;
    const mask = bits === 0 ? 0 : (~0 << (32 - bits)) >>> 0;
    return (ip4 & mask) === (net4 & mask);
  }

  const ip6 = ipv6ToBigInt(ip);
  const net6 = ipv6ToBigInt(netIp);
  if (ip6 !== null && net6 !== null) {
    const bits = prefixRaw === "" ? 128 : Number(prefixRaw);
    if (!Number.isInteger(bits) || bits < 0 || bits > 128) return false;
    const shift = BigInt(128 - bits);
    return (ip6 >> shift) === (net6 >> shift);
  }
  return false;
}

export function isIpInCidrs(rawIp: string, cidrs: string[]): boolean {
  if (!rawIp || cidrs.length === 0) return false;
  return cidrs.some((c) => isIpInCidr(rawIp, c));
}

export interface ClientIpOptions {
  xForwardedFor?: string | null;
  xRealIp?: string | null;
  remoteAddr?: string | null;
  trustProxy: boolean;
  trustedProxies: string[];
}

export function resolveTrustedClientIp(opts: ClientIpOptions): string {
  const remote = normalizeIp(opts.remoteAddr || "");
  if (!opts.trustProxy || opts.trustedProxies.length === 0) {
    return isValidIp(remote) ? remote : "direct";
  }

  // Only inspect forwarded headers when either the direct socket peer matches a
  // trusted proxy CIDR, or in serverless/Next adapters where socket remoteAddr is
  // absent while proxy trust was explicitly configured with trusted proxy CIDRs.
  if (remote && isValidIp(remote) && !isIpInCidrs(remote, opts.trustedProxies)) {
    return remote;
  }

  if (opts.xForwardedFor) {
    const parts = opts.xForwardedFor
      .split(",")
      .map((s) => normalizeIp(s))
      .filter((s) => isValidIp(s));
    for (let i = parts.length - 1; i >= 0; i -= 1) {
      if (!isIpInCidrs(parts[i], opts.trustedProxies)) {
        return parts[i];
      }
    }
    if (parts.length > 0) {
      return parts[0];
    }
  }

  if (opts.xRealIp) {
    const real = normalizeIp(opts.xRealIp);
    if (isValidIp(real)) {
      return real;
    }
  }

  return isValidIp(remote) ? remote : "direct";
}

function getTrustedProxyEnv(): { trustProxy: boolean; trustedProxies: string[] } {
  const trustProxy =
    (process.env.LOVELACE_TRUST_PROXY || "").trim().toLowerCase() === "true";
  const trustedProxies = (process.env.LOVELACE_TRUSTED_PROXIES || "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  return { trustProxy, trustedProxies };
}

const bffGlobalLimiter = new SlidingFixedWindowLimiter(500, 60_000);
const bffReadLimiter = new SlidingFixedWindowLimiter(120, 60_000);
const bffWriteLimiter = new SlidingFixedWindowLimiter(25, 60_000);
const bffLookupLimiter = new SlidingFixedWindowLimiter(15, 60_000);

export type BffLimitTier = "read" | "write" | "lookup";

export function checkBffRateLimit(
  req: Request,
  tier: BffLimitTier,
): { allowed: boolean; retryAfterSeconds: number; clientIp: string } {
  const { trustProxy, trustedProxies } = getTrustedProxyEnv();
  const clientIp = resolveTrustedClientIp({
    xForwardedFor: req.headers.get("x-forwarded-for"),
    xRealIp: req.headers.get("x-real-ip"),
    remoteAddr: (req as unknown as { ip?: string }).ip || "",
    trustProxy,
    trustedProxies,
  });

  const now = Date.now();
  const globalCheck = bffGlobalLimiter.allow("global", now);
  if (!globalCheck.allowed) {
    return { allowed: false, retryAfterSeconds: globalCheck.retryAfterSeconds, clientIp };
  }

  const tierLimiter =
    tier === "lookup"
      ? bffLookupLimiter
      : tier === "write"
        ? bffWriteLimiter
        : bffReadLimiter;
  const ipKey = clientIp === "direct" ? "direct" : clientIp;
  const clientCheck = tierLimiter.allow(`${tier}:${ipKey}`, now);
  return {
    allowed: clientCheck.allowed,
    retryAfterSeconds: clientCheck.retryAfterSeconds,
    clientIp: isValidIp(clientIp) ? clientIp : "",
  };
}

export function rateLimitResponse(retryAfterSeconds: number) {
  return NextResponse.json(
    {
      error: {
        code: "rate_limited",
        message: "Too many requests. Please wait a moment and try again.",
      },
    },
    {
      status: 429,
      headers: {
        "Retry-After": String(Math.max(1, retryAfterSeconds)),
        "Cache-Control": "no-store",
      },
    },
  );
}
