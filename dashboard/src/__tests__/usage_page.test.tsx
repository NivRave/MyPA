import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import UsagePage from '@/app/usage/page';
import { againstCap } from '@/lib/usage';
import { graphqlFetch, type Usage } from '@/lib/graphql';

vi.mock('@/lib/graphql', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/graphql')>();
  return { ...actual, graphqlFetch: vi.fn() };
});

const usage = (over: Partial<Usage> = {}): Usage => ({
  timeZone: 'Asia/Jerusalem',
  personDailyCap: 1000,
  installDailyCap: 50000,
  myTokensToday: 1000,
  installTokensToday: 10285,
  days: [
    { day: '2026-11-21', personId: 'p-dana', name: 'Dana', inputTokens: 900, outputTokens: 100, calls: 3, turns: 2 },
    { day: '2026-11-21', personId: 'p-noam', name: 'Noam', inputTokens: 50, outputTokens: 5, calls: 1, turns: 1 },
  ],
  turns: [
    { turnId: 't-1', personId: 'p-dana', name: 'Dana', startedAt: '2026-11-21T10:00:00Z', inputTokens: 300, outputTokens: 30, calls: 2, providers: 'anthropic,gemini' },
  ],
  ...over,
});

// V4-04: the dashboard shows usage against the daily caps, per person and day, and per turn.
describe('Usage page', () => {
  const mocked = vi.mocked(graphqlFetch);
  beforeEach(() => vi.clearAllMocks());

  it('shows today against the caps, by day and by turn', async () => {
    mocked.mockResolvedValueOnce({ usage: usage() });
    render(await UsagePage());

    expect(screen.getByTestId('my-cap').textContent).toContain('1,000 of 1,000 tokens');
    expect(screen.getByTestId('my-cap').textContent).toContain('Reached');
    expect(screen.getByTestId('install-cap').textContent).toContain('10,285 of 50,000 tokens');
    expect(screen.getByText(/00:00 in Asia\/Jerusalem/)).toBeInTheDocument();
    expect(screen.getByText('Noam')).toBeInTheDocument();
    expect(screen.getByText('anthropic,gemini')).toBeInTheDocument();
    expect(screen.getByText('300 / 30')).toBeInTheDocument();
  });

  it('shows the install total only when the API gives it (the operator)', async () => {
    mocked.mockResolvedValueOnce({ usage: usage({ installTokensToday: null, myTokensToday: 55 }) });
    render(await UsagePage());

    expect(screen.queryByTestId('install-cap')).toBeNull();
    expect(screen.getByTestId('my-cap').textContent).not.toContain('Reached');
  });

  it('says when nothing is capped or nothing was used', async () => {
    mocked.mockResolvedValueOnce({ usage: usage({ personDailyCap: 0, myTokensToday: 0, installTokensToday: null, days: [], turns: [] }) });
    render(await UsagePage());

    expect(screen.getByTestId('my-cap').textContent).toContain('0 tokens (no cap)');
    expect(screen.getByText(/No usage in the last 14 days/)).toBeInTheDocument();
    expect(screen.getByText(/No turns yet/)).toBeInTheDocument();
  });

  it('says so when usage cannot be loaded', async () => {
    mocked.mockRejectedValueOnce(new Error('down'));
    vi.spyOn(console, 'error').mockImplementation(() => {});
    render(await UsagePage());
    expect(screen.getByText(/could not be loaded/)).toBeInTheDocument();
  });

  it('formats a cap', () => {
    expect(againstCap(1234, 5000)).toBe('1,234 of 5,000 tokens');
    expect(againstCap(7, 0)).toBe('7 tokens (no cap)');
  });
});
