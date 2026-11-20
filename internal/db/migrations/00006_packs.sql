-- Feature packs (V4-27, DE-6), additive only: the household stores which opt-in packs it has
-- switched on, as a sorted comma-separated list of pack IDs (internal/packs). '' is Core only, which
-- is where a household created from now on starts.
--
-- On upgrade every pack that already holds data for a household is switched on, so nothing a family
-- was using disappears:
--   baby         a baby profile, care log or inventory item
--   house        a contract or receipt, a maintenance item or a vehicle
--   finance      a transaction, a category budget or a recurring bill
--   automations  an API connector, an automation rule, or a workflow of one of its members
--   gmail        a logged Gmail action (topic 'communication'), or a member with a stored Google
--                sign-in: Gmail keeps no rows of its own, and a connected Google account is what
--                Gmail used
-- A person's row (workflow, Google sign-in) counts for the household of their users row; a row
-- whose person_id is empty is matched by the old key, the platform ID.

-- +goose Up
ALTER TABLE public.households ADD COLUMN packs text DEFAULT ''::text NOT NULL;

WITH used AS (
    SELECT household_id, 'baby' AS pack FROM public.baby_profiles
    UNION SELECT household_id, 'baby' FROM public.baby_care_logs
    UNION SELECT household_id, 'baby' FROM public.baby_inventory_items
    UNION SELECT household_id, 'house' FROM public.house_contract_records
    UNION SELECT household_id, 'house' FROM public.home_maintenance_items
    UNION SELECT household_id, 'house' FROM public.vehicle_assets
    UNION SELECT household_id, 'finance' FROM public.financial_transactions
    UNION SELECT household_id, 'finance' FROM public.category_budgets
    UNION SELECT household_id, 'finance' FROM public.recurring_bills
    UNION SELECT household_id, 'automations' FROM public.external_api_connectors
    UNION SELECT household_id, 'automations' FROM public.automation_rules
    UNION SELECT u.household_id, 'automations' FROM public.workflows w
        JOIN public.users u ON u.id = w.person_id OR (w.person_id IS NULL AND u.platform_id = w.user_id)
    UNION SELECT household_id, 'gmail' FROM public.mutation_logs WHERE topic = 'communication'
    UNION SELECT u.household_id, 'gmail' FROM public.encrypted_credentials c
        JOIN public.users u ON u.id = c.person_id OR (c.person_id IS NULL AND u.platform_id = c.user_id)
        WHERE c.provider = 'google_oauth'
)
UPDATE public.households h
SET packs = sub.list
FROM (
    SELECT household_id, string_agg(pack, ',' ORDER BY pack) AS list
    FROM (SELECT DISTINCT household_id, pack FROM used WHERE household_id IS NOT NULL) d
    GROUP BY household_id
) sub
WHERE h.id = sub.household_id;

-- +goose Down
ALTER TABLE public.households DROP COLUMN packs;
