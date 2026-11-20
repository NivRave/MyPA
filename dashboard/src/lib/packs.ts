/**
 * Feature packs in the dashboard (V4-27). Which routes belong to which pack comes from the API
 * (Pack.pages), so the dashboard keeps no second copy of the catalogue.
 */
import type { Me, Pack } from "./graphql";

/** The pack that owns a route (the route itself or a page below it), or undefined for Core. */
export function packOfPage(packs: Pack[], route: string): Pack | undefined {
  return packs.find((p) => p.pages.some((pg) => route === pg || route.startsWith(pg + "/")));
}

/**
 * Whether a route is shown: a Core page always, a pack's page only while the pack is on. Without a
 * signed-in person nothing is known about packs, so every page shows and asks them to sign in.
 */
export function pageEnabled(me: Me | null, route: string): boolean {
  if (!me) return true;
  const pack = packOfPage(me.household.packs ?? [], route);
  return !pack || pack.enabled;
}

/** Every topic any household can have, for a page shown without a signed-in person. */
const ALL_TOPICS = ["family", "health", "personal", "work", "baby", "house", "finance"];

/** The topics that default to shared within a household (models.IsDefaultSharedTopic). */
const DEFAULT_SHARED = ["baby", "house", "finance", "family"];

/** The household's topics (V4-27): Core's plus those of its packs that are on. */
export function householdTopics(me: Me | null): string[] {
  return me?.household.topics ?? ALL_TOPICS;
}

/** The household's topics that default to shared, in topic order. */
export function defaultSharedTopics(me: Me | null): string[] {
  return householdTopics(me).filter((t) => DEFAULT_SHARED.includes(t));
}

/** "health" → "Health". */
export function topicLabel(topic: string): string {
  return topic.charAt(0).toUpperCase() + topic.slice(1);
}
