import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { handleSignIn, handleSignOut, sameOrigin } from '@/lib/auth_routes';
import { isSecureRequest, isWellFormedSecret, sessionFrom } from '@/lib/session';
import SignInPage from '@/app/signin/page';

// V4-35 (ADR 0002 Decision 5): the dashboard's side of sign-in.

const TOKEN = 'TtTtTtTtTtTtTtTtTtTtTtTtTtTtTtTtTtTtTtTt-_x';
const SESSION = 'SsSsSsSsSsSsSsSsSsSsSsSsSsSsSsSsSsSsSsSs-_y';

function signInRequest(body: string, headers: Record<string, string> = {}, url = 'http://localhost:3030/api/auth/signin') {
  return new Request(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Origin: 'http://localhost:3030', Host: 'localhost:3030', ...headers },
    body,
  });
}

function appAnswers(status: number, body: unknown) {
  return vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status }));
}

describe('session helpers', () => {
  it('accepts only a 256-bit base64url secret', () => {
    expect(isWellFormedSecret(SESSION)).toBe(true);
    for (const v of ['', 'short', SESSION + 'A', SESSION.slice(1), '!'.repeat(43), undefined, null]) {
      expect(isWellFormedSecret(v)).toBe(false);
    }
  });

  it('reads either cookie name, ignoring a malformed one', () => {
    const jar = (m: Record<string, string>) => ({ get: (n: string) => (n in m ? { value: m[n] } : undefined) });
    expect(sessionFrom(jar({ mypa_session: SESSION }))).toBe(SESSION);
    expect(sessionFrom(jar({ '__Host-mypa_session': SESSION }))).toBe(SESSION);
    expect(sessionFrom(jar({ mypa_session: 'x' }))).toBeUndefined();
  });

  it('tells HTTPS from plain HTTP, trusting the proxy header when there is one', () => {
    expect(isSecureRequest('http://localhost:3030/x', null)).toBe(false);
    expect(isSecureRequest('https://home.example/x', null)).toBe(true);
    expect(isSecureRequest('http://dashboard:3000/x', 'https')).toBe(true);
  });
});

describe('POST /api/auth/signin', () => {
  beforeEach(() => {
    process.env.GRAPHQL_ENDPOINT = 'http://app:8000/graphql';
  });

  it('exchanges the token at the app and puts the session in an HttpOnly, SameSite=Lax cookie', async () => {
    const app = appAnswers(200, { session: SESSION, maxAgeSeconds: 604800 });
    const res = await handleSignIn(signInRequest(JSON.stringify({ token: TOKEN }), { 'X-Forwarded-For': '192.0.2.7' }), app);
    expect(res.status).toBe(200);
    const [url, init] = app.mock.calls[0];
    expect(url).toBe('http://app:8000/auth/dashboard/signin');
    expect(JSON.parse(init.body)).toEqual({ token: TOKEN });
    expect(init.headers['X-Forwarded-For']).toBe('192.0.2.7');
    const cookie = res.headers.get('set-cookie') ?? '';
    expect(cookie).toContain(`mypa_session=${SESSION}`);
    expect(cookie).toContain('HttpOnly');
    expect(cookie).toContain('SameSite=Lax');
    expect(cookie).toContain('Path=/');
    expect(cookie).toContain('Max-Age=604800');
    expect(cookie).not.toContain('Secure');
    expect(cookie).not.toContain('Domain');
    expect(await res.text()).not.toContain(SESSION);
  });

  it('on HTTPS the cookie is Secure and carries the __Host- prefix', async () => {
    const app = appAnswers(200, { session: SESSION, maxAgeSeconds: 604800 });
    const req = signInRequest(JSON.stringify({ token: TOKEN }), { Origin: 'https://home.example', Host: 'home.example', 'X-Forwarded-Proto': 'https' }, 'http://dashboard:3000/api/auth/signin');
    const cookie = (await handleSignIn(req, app)).headers.get('set-cookie') ?? '';
    expect(cookie).toMatch(/^__Host-mypa_session=/);
    expect(cookie).toContain('Secure');
  });

  it('refuses another site, a form post, a malformed token, and a token the app refuses', async () => {
    const app = appAnswers(401, { error: 'no' });
    const cases: Array<[Request, number]> = [
      [signInRequest(JSON.stringify({ token: TOKEN }), { Origin: 'https://evil.example' }), 403],
      [signInRequest(JSON.stringify({ token: TOKEN }), { Origin: '' }), 403],
      [signInRequest(`token=${TOKEN}`, { 'Content-Type': 'application/x-www-form-urlencoded' }), 415],
      [signInRequest(JSON.stringify({ token: 'short' })), 400],
      [signInRequest('not json'), 400],
    ];
    for (const [req, status] of cases) {
      const res = await handleSignIn(req, app);
      expect(res.status).toBe(status);
      expect(res.headers.get('set-cookie')).toBeNull();
    }
    expect(app).not.toHaveBeenCalled();
    const refused = await handleSignIn(signInRequest(JSON.stringify({ token: TOKEN })), app);
    expect(refused.status).toBe(401);
    expect(refused.headers.get('set-cookie')).toBeNull();
  });
});

describe('POST /api/auth/signout', () => {
  it('ends the session at the app and clears the cookie', async () => {
    process.env.GRAPHQL_ENDPOINT = 'http://app:8000/graphql';
    const app = appAnswers(200, { data: { signOut: true } });
    const req = new Request('http://localhost:3030/api/auth/signout', {
      method: 'POST',
      headers: { Origin: 'http://localhost:3030', Host: 'localhost:3030', Cookie: `mypa_session=${SESSION}` },
    });
    const res = await handleSignOut(req, app);
    expect(res.status).toBe(303);
    expect(res.headers.get('location')).toBe('/signin?signedOut=1');
    expect(res.headers.get('set-cookie')).toContain('mypa_session=;');
    expect(res.headers.get('set-cookie')).toContain('Max-Age=0');
    expect(app.mock.calls[0][1].headers.Authorization).toBe(`Bearer ${SESSION}`);
    expect(app.mock.calls[0][1].body).toContain('signOut');
  });

  it('refuses a sign-out from another site', async () => {
    const req = new Request('http://localhost:3030/api/auth/signout', { method: 'POST', headers: { Origin: 'https://evil.example', Host: 'localhost:3030' } });
    expect(sameOrigin(req)).toBe(false);
    expect((await handleSignOut(req, vi.fn())).status).toBe(403);
  });
});

describe('the sign-in page', () => {
  const originalFetch = global.fetch;
  afterEach(() => {
    global.fetch = originalFetch;
    window.history.replaceState(null, '', '/');
  });

  it('opening the link sends nothing; only the button submits the token, which leaves the address bar', async () => {
    const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 401 }));
    global.fetch = mockFetch;
    window.history.replaceState(null, '', `/signin#${TOKEN}`);

    render(<SignInPage />);
    const button = await screen.findByRole('button', { name: 'Sign in' });
    expect(mockFetch).not.toHaveBeenCalled();
    expect(window.location.hash).toBe('');

    fireEvent.click(button);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));
    const [url, init] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/auth/signin');
    expect(JSON.parse(init.body)).toEqual({ token: TOKEN });
    expect(await screen.findByText(/does not work any more/)).toBeInTheDocument();
  });

  it('without a token it says how to get a link', async () => {
    window.history.replaceState(null, '', '/signin');
    render(<SignInPage />);
    expect(await screen.findByText(/to the assistant in a direct chat/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Sign in' })).toBeNull();
  });
});
