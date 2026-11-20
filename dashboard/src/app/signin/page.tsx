"use client";

import { useEffect, useState } from "react";

// V4-35 (ADR 0002 Decision 5): a /dashboard link opens this page with its token after `#`. The
// part after `#` never reaches a server, so a chat app's link preview can neither use nor log it.
// Opening the page changes nothing: the token is sent only when the person presses the button.

type State = "loading" | "ready" | "none" | "signingIn" | "failed";

const TOKEN = /^[A-Za-z0-9_-]{43}$/;

export default function SignInPage() {
  const [token, setToken] = useState<string | null>(null);
  const [state, setState] = useState<State>("loading");
  const [signedOut, setSignedOut] = useState(false);

  useEffect(() => {
    // The fragment exists only in the browser, so it is read once after the first render (the
    // server render shows "loading"); this is the one place the page syncs with the address bar.
    const t = window.location.hash.replace(/^#/, "");
    const out = new URLSearchParams(window.location.search).has("signedOut");
    // Take the token out of the address bar and the history at once.
    if (window.location.hash) {
      window.history.replaceState(null, "", window.location.pathname);
    }
    const ok = TOKEN.test(t);
    // eslint-disable-next-line react-hooks/set-state-in-effect -- reading the URL fragment on mount (see above)
    setState(ok ? "ready" : "none");
    setToken(ok ? t : null);
    setSignedOut(out);
  }, []);

  async function signIn() {
    if (!token) {
      return;
    }
    setState("signingIn");
    const res = await fetch("/api/auth/signin", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token }),
    }).catch(() => null);
    setToken(null);
    if (res && res.ok) {
      window.location.replace("/");
    } else {
      setState("failed");
    }
  }

  return (
    <div className="flex-1 flex items-center justify-center p-8">
      <div className="max-w-md w-full bg-white rounded-xl shadow-sm border border-neutral-200 p-8 space-y-4">
        <h1 className="text-xl font-semibold text-neutral-900">Sign in to MyPA</h1>
        {signedOut && state !== "ready" && <p className="text-sm text-emerald-700">You are signed out.</p>}
        {state === "ready" && (
          <>
            <p className="text-sm text-neutral-600">
              This link signs you in as yourself. Only continue if you asked the assistant for it just now.
            </p>
            <button
              type="button"
              onClick={signIn}
              className="w-full bg-neutral-900 text-white rounded-md py-2 text-sm font-medium hover:bg-neutral-800"
            >
              Sign in
            </button>
          </>
        )}
        {state === "signingIn" && <p className="text-sm text-neutral-600">Signing you in…</p>}
        {(state === "none" || state === "failed") && (
          <>
            {state === "failed" && (
              <p className="text-sm text-red-600">This sign-in link does not work any more: it was used, or more than 10 minutes passed.</p>
            )}
            <p className="text-sm text-neutral-600">
              To sign in, send <code className="font-mono bg-neutral-100 px-1 rounded">/dashboard</code> to the assistant in a direct chat and open the link it
              sends you, in a browser on this computer. The link works once, for 10 minutes.
            </p>
          </>
        )}
      </div>
    </div>
  );
}
