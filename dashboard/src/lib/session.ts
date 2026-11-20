/**
 * Dashboard sign-in (V4-35, ADR 0002 Decision 5).
 *
 * The browser holds the session secret in an HttpOnly, SameSite=Lax cookie on the dashboard's
 * own address. The dashboard's server reads it and sends it to the app as a bearer value; the
 * app never sees the cookie, and page scripts can never read it. On a plain local address
 * (http://localhost:3030) the cookie is not Secure; on HTTPS it is Secure and carries the
 * __Host- prefix.
 */

/** The cookie on a plain (http) address, e.g. the home install's http://localhost:3030. */
export const SESSION_COOKIE = "mypa_session";
/** The cookie on an HTTPS address: Secure, no Domain, path / (the __Host- prefix requires it). */
export const SECURE_SESSION_COOKIE = "__Host-mypa_session";

/** A session secret is 32 random bytes, base64url without padding. */
const SECRET_PATTERN = /^[A-Za-z0-9_-]{43}$/;

export function isWellFormedSecret(value: string | undefined | null): value is string {
  return typeof value === "string" && SECRET_PATTERN.test(value);
}

interface CookieReader {
  get(name: string): { value: string } | undefined;
}

/** The session secret the browser sent, from either cookie name, if it is well formed. */
export function sessionFrom(cookies: CookieReader): string | undefined {
  for (const name of [SECURE_SESSION_COOKIE, SESSION_COOKIE]) {
    const v = cookies.get(name)?.value;
    if (isWellFormedSecret(v)) {
      return v;
    }
  }
  return undefined;
}

/**
 * Whether the request reached the dashboard over HTTPS: the request's own scheme, or the one a
 * reverse proxy in front of it reports.
 */
export function isSecureRequest(url: string, forwardedProto: string | null): boolean {
  const proto = (forwardedProto ?? "").split(",")[0].trim().toLowerCase();
  if (proto) {
    return proto === "https";
  }
  return url.toLowerCase().startsWith("https:");
}

/** The app's base address, for the sign-in exchange (the same app /graphql is on). */
export function getAppBaseURL(graphqlEndpoint: string): string {
  return graphqlEndpoint.replace(/\/graphql\/?$/, "");
}
