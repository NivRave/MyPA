import { handleSignIn } from "@/lib/auth_routes";

// V4-35: exchanges the token a /dashboard link carries for a session cookie.
export async function POST(request: Request) {
  return handleSignIn(request);
}
