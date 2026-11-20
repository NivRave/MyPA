import { handleSignOut } from "@/lib/auth_routes";

// V4-35: ends the dashboard session and clears its cookie.
export async function POST(request: Request) {
  return handleSignOut(request);
}
