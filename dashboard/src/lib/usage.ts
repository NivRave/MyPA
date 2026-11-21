/** Usage figures for the dashboard (V4-04). */

export const formatTokens = (n: number) => n.toLocaleString("en-US");

/** "1,234 of 5,000 tokens", or "1,234 tokens (no cap)" when the cap is 0. */
export function againstCap(used: number, cap: number): string {
  return cap > 0 ? `${formatTokens(used)} of ${formatTokens(cap)} tokens` : `${formatTokens(used)} tokens (no cap)`;
}
