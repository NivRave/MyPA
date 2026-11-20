/**
 * The dashboard's sign-in and sign-out handlers (V4-35), kept apart from the route files so they
 * can be tested without a running Next.js server.
 */
import { getGraphQLEndpoint } from "./graphql";
import {
  SECURE_SESSION_COOKIE,
  SESSION_COOKIE,
  getAppBaseURL,
  isSecureRequest,
  isWellFormedSecret,
  sessionFrom,
} from "./session";

const REFUSED = "This sign-in link does not work any more. Ask for a new one with /dashboard in a direct chat with the assistant.";

/**
 * A request from another site is refused: the browser sends Origin on every POST it makes for a
 * page, so a form or script on another site cannot sign the person into someone else's account
 * (login CSRF) or sign them out.
 */
export function sameOrigin(request: Request): boolean {
  const origin = request.headers.get("origin");
  if (!origin) {
    return false;
  }
  try {
    const host = request.headers.get("x-forwarded-host") ?? request.headers.get("host") ?? new URL(request.url).host;
    return new URL(origin).host === host;
  } catch {
    return false;
  }
}

function cookieHeader(name: string, value: string, maxAge: number, secure: boolean): string {
  const parts = [`${name}=${value}`, "Path=/", "HttpOnly", "SameSite=Lax", `Max-Age=${maxAge}`];
  if (secure) {
    parts.push("Secure");
  }
  return parts.join("; ");
}

function json(status: number, body: unknown, extra?: Record<string, string>): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", "Cache-Control": "no-store", ...extra },
  });
}

/**
 * POST /api/auth/signin {"token": "..."}: the page sends the token it read after `#` when the
 * person presses the button. The token is exchanged at the app for a session, and the session goes
 * into the cookie; the page never sees it.
 */
export async function handleSignIn(request: Request, fetchImpl: typeof fetch = fetch): Promise<Response> {
  if (!sameOrigin(request)) {
    return json(403, { error: REFUSED });
  }
  if (!(request.headers.get("content-type") ?? "").toLowerCase().startsWith("application/json")) {
    return json(415, { error: REFUSED });
  }
  let token: unknown;
  try {
    token = ((await request.json()) as { token?: unknown }).token;
  } catch {
    return json(400, { error: REFUSED });
  }
  if (typeof token !== "string" || !isWellFormedSecret(token)) {
    return json(400, { error: REFUSED });
  }

  const forwarded = request.headers.get("x-forwarded-for");
  const res = await fetchImpl(`${getAppBaseURL(getGraphQLEndpoint())}/auth/dashboard/signin`, {
    method: "POST",
    cache: "no-store",
    headers: { "Content-Type": "application/json", ...(forwarded ? { "X-Forwarded-For": forwarded } : {}) },
    body: JSON.stringify({ token }),
  }).catch(() => null);
  if (!res || !res.ok) {
    return json(res?.status === 403 ? 403 : 401, { error: REFUSED });
  }
  const body = (await res.json().catch(() => ({}))) as { session?: string; maxAgeSeconds?: number };
  if (!isWellFormedSecret(body.session)) {
    return json(502, { error: REFUSED });
  }
  const secure = isSecureRequest(request.url, request.headers.get("x-forwarded-proto"));
  const name = secure ? SECURE_SESSION_COOKIE : SESSION_COOKIE;
  const maxAge = typeof body.maxAgeSeconds === "number" && body.maxAgeSeconds > 0 ? body.maxAgeSeconds : 7 * 24 * 3600;
  return json(200, { ok: true }, { "Set-Cookie": cookieHeader(name, body.session, maxAge, secure) });
}

/**
 * POST /api/auth/signout: ends the session at the app (signOut) and clears the cookie, then
 * returns to the sign-in page.
 */
export async function handleSignOut(request: Request, fetchImpl: typeof fetch = fetch): Promise<Response> {
  if (!sameOrigin(request)) {
    return json(403, { error: "Sign out from the dashboard itself." });
  }
  const jar = new Map<string, string>();
  for (const part of (request.headers.get("cookie") ?? "").split(";")) {
    const i = part.indexOf("=");
    if (i > 0) {
      jar.set(part.slice(0, i).trim(), part.slice(i + 1).trim());
    }
  }
  const session = sessionFrom({ get: (n: string) => (jar.has(n) ? { value: jar.get(n) as string } : undefined) });
  if (session) {
    await fetchImpl(getGraphQLEndpoint(), {
      method: "POST",
      cache: "no-store",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${session}` },
      body: JSON.stringify({ query: "mutation SignOut { signOut }" }),
    }).catch(() => null);
  }
  const secure = isSecureRequest(request.url, request.headers.get("x-forwarded-proto"));
  const headers = new Headers({ Location: "/signin?signedOut=1", "Cache-Control": "no-store" });
  headers.append("Set-Cookie", cookieHeader(SESSION_COOKIE, "", 0, false));
  if (secure) {
    headers.append("Set-Cookie", cookieHeader(SECURE_SESSION_COOKIE, "", 0, true));
  }
  return new Response(null, { status: 303, headers });
}
