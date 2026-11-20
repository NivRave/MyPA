import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";
import { sessionFrom } from "@/lib/session";

// V4-35: without a session cookie, every page sends the person to /signin. This is only the
// quick check; the app validates the session on every request and refuses an ended one.
export function proxy(request: NextRequest) {
  if (sessionFrom(request.cookies)) {
    return NextResponse.next();
  }
  return NextResponse.redirect(new URL("/signin", request.url));
}

export const config = {
  // Everything but the sign-in page, its API routes and static files.
  matcher: ["/((?!signin|api/auth|_next/static|_next/image|favicon.ico).*)"],
};
