-- Migration A of ADR 0002 (V4-30): households, additive only.
--
-- A household replaces the free-text family label. One household per distinct label on users and
-- pairing_codes, compared after trimming and otherwise exactly, as SQL compared it before; two
-- labels that differ only in case stay two households, because merging them would join two
-- tenants. A user without a label gets a household of their own, keyed by their platform ID, which
-- is the tenant key the code wrote for them (DE-2), so their rows map to it. A label found only on
-- data rows becomes a household with no members.
--
-- A row with an empty label stays where it was visible: a row its owner could read before (the
-- tables whose reads matched user_id whatever the label, for example the personal memories that
-- P2-4 copied with an empty label) goes to its owner's household; every other one goes to one
-- memberless holding household, so it stays as invisible as it is today (DP-26).
--
-- household_id is added (NOT NULL, foreign key, indexed) to the 20 tables that carry a label.
-- family_group stays and is still written; V4-44 drops it after a release in use.

-- +goose Up
CREATE TABLE public.households (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    legacy_label text,
    language text DEFAULT ''::text NOT NULL,
    time_zone text DEFAULT ''::text NOT NULL,
    currency text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);
ALTER TABLE ONLY public.households ADD CONSTRAINT households_pkey PRIMARY KEY (id);
CREATE UNIQUE INDEX idx_households_legacy_label ON public.households USING btree (legacy_label);

-- 1. One household per label on users and pairing codes.
INSERT INTO public.households (name, legacy_label, created_at, updated_at)
SELECT l, l, now(), now() FROM (
    SELECT btrim(family_group) AS l FROM public.users
    UNION
    SELECT btrim(family_group) FROM public.pairing_codes
) labels
WHERE l <> ''
ORDER BY l;

-- 2. A household of their own for every user without a label, keyed by their platform ID. A label
--    equal to that platform ID already named the same tenant, so it is the same household.
INSERT INTO public.households (name, legacy_label, created_at, updated_at)
SELECT COALESCE(NULLIF(btrim(name), ''), 'Household'), platform_id, now(), now()
FROM public.users
WHERE COALESCE(btrim(family_group), '') = '' AND COALESCE(platform_id, '') <> ''
ORDER BY created_at, platform_id
ON CONFLICT (legacy_label) DO NOTHING;

-- 3. A memberless household for every label found only on data rows.
INSERT INTO public.households (name, legacy_label, created_at, updated_at)
SELECT l, l, now(), now() FROM (
    SELECT btrim(family_group) AS l FROM public.automation_rules
    UNION
    SELECT btrim(family_group) AS l FROM public.baby_care_logs
    UNION
    SELECT btrim(family_group) AS l FROM public.baby_inventory_items
    UNION
    SELECT btrim(family_group) AS l FROM public.baby_profiles
    UNION
    SELECT btrim(family_group) AS l FROM public.category_budgets
    UNION
    SELECT btrim(family_group) AS l FROM public.entity_profiles
    UNION
    SELECT btrim(family_group) AS l FROM public.external_api_connectors
    UNION
    SELECT btrim(family_group) AS l FROM public.financial_transactions
    UNION
    SELECT btrim(family_group) AS l FROM public.home_maintenance_items
    UNION
    SELECT btrim(family_group) AS l FROM public.house_contract_records
    UNION
    SELECT btrim(family_group) AS l FROM public.mutation_logs
    UNION
    SELECT btrim(family_group) AS l FROM public.personal_crm_contacts
    UNION
    SELECT btrim(family_group) AS l FROM public.recurring_bills
    UNION
    SELECT btrim(family_group) AS l FROM public.schedule_events
    UNION
    SELECT btrim(family_group) AS l FROM public.shared_spaces
    UNION
    SELECT btrim(family_group) AS l FROM public.shared_task_reminders
    UNION
    SELECT btrim(family_group) AS l FROM public.topic_memories
    UNION
    SELECT btrim(family_group) AS l FROM public.vehicle_assets
) labels
WHERE l <> ''
ORDER BY l
ON CONFLICT (legacy_label) DO NOTHING;

-- 4. household_id on every labelled table, filled by label. Users first: an unlabelled user's
--    household is keyed by their platform ID.

ALTER TABLE public.users ADD COLUMN household_id uuid;
UPDATE public.users x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);
UPDATE public.users x SET household_id = h.id FROM public.households h
    WHERE x.household_id IS NULL AND COALESCE(btrim(x.family_group), '') = ''
      AND COALESCE(x.platform_id, '') <> '' AND h.legacy_label = x.platform_id;

ALTER TABLE public.pairing_codes ADD COLUMN household_id uuid;
UPDATE public.pairing_codes x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.automation_rules ADD COLUMN household_id uuid;
UPDATE public.automation_rules x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.baby_care_logs ADD COLUMN household_id uuid;
UPDATE public.baby_care_logs x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.baby_inventory_items ADD COLUMN household_id uuid;
UPDATE public.baby_inventory_items x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.baby_profiles ADD COLUMN household_id uuid;
UPDATE public.baby_profiles x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.category_budgets ADD COLUMN household_id uuid;
UPDATE public.category_budgets x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.entity_profiles ADD COLUMN household_id uuid;
UPDATE public.entity_profiles x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.external_api_connectors ADD COLUMN household_id uuid;
UPDATE public.external_api_connectors x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.financial_transactions ADD COLUMN household_id uuid;
UPDATE public.financial_transactions x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.home_maintenance_items ADD COLUMN household_id uuid;
UPDATE public.home_maintenance_items x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.house_contract_records ADD COLUMN household_id uuid;
UPDATE public.house_contract_records x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.mutation_logs ADD COLUMN household_id uuid;
UPDATE public.mutation_logs x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.personal_crm_contacts ADD COLUMN household_id uuid;
UPDATE public.personal_crm_contacts x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.recurring_bills ADD COLUMN household_id uuid;
UPDATE public.recurring_bills x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.schedule_events ADD COLUMN household_id uuid;
UPDATE public.schedule_events x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.shared_spaces ADD COLUMN household_id uuid;
UPDATE public.shared_spaces x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.shared_task_reminders ADD COLUMN household_id uuid;
UPDATE public.shared_task_reminders x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.topic_memories ADD COLUMN household_id uuid;
UPDATE public.topic_memories x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

ALTER TABLE public.vehicle_assets ADD COLUMN household_id uuid;
UPDATE public.vehicle_assets x SET household_id = h.id FROM public.households h
    WHERE btrim(x.family_group) <> '' AND h.legacy_label = btrim(x.family_group);

-- 5. A row without a label that its owner could read goes to the owner's household. The owner is
--    named by platform ID, or by users.id where a module wrote that (finance).
UPDATE public.automation_rules x SET household_id = u.household_id FROM public.users u
    WHERE x.household_id IS NULL AND COALESCE(btrim(x.family_group), '') = ''
      AND COALESCE(x.user_id, '') <> '' AND u.household_id IS NOT NULL
      AND (u.platform_id = x.user_id OR u.id::text = x.user_id);
UPDATE public.entity_profiles x SET household_id = u.household_id FROM public.users u
    WHERE x.household_id IS NULL AND COALESCE(btrim(x.family_group), '') = ''
      AND COALESCE(x.user_id, '') <> '' AND u.household_id IS NOT NULL
      AND (u.platform_id = x.user_id OR u.id::text = x.user_id);
UPDATE public.external_api_connectors x SET household_id = u.household_id FROM public.users u
    WHERE x.household_id IS NULL AND COALESCE(btrim(x.family_group), '') = ''
      AND COALESCE(x.user_id, '') <> '' AND u.household_id IS NOT NULL
      AND (u.platform_id = x.user_id OR u.id::text = x.user_id);
UPDATE public.financial_transactions x SET household_id = u.household_id FROM public.users u
    WHERE x.household_id IS NULL AND COALESCE(btrim(x.family_group), '') = ''
      AND COALESCE(x.user_id, '') <> '' AND u.household_id IS NOT NULL
      AND (u.platform_id = x.user_id OR u.id::text = x.user_id);
UPDATE public.mutation_logs x SET household_id = u.household_id FROM public.users u
    WHERE x.household_id IS NULL AND COALESCE(btrim(x.family_group), '') = ''
      AND COALESCE(x.user_id, '') <> '' AND u.household_id IS NOT NULL
      AND (u.platform_id = x.user_id OR u.id::text = x.user_id);
UPDATE public.personal_crm_contacts x SET household_id = u.household_id FROM public.users u
    WHERE x.household_id IS NULL AND COALESCE(btrim(x.family_group), '') = ''
      AND COALESCE(x.user_id, '') <> '' AND u.household_id IS NOT NULL
      AND (u.platform_id = x.user_id OR u.id::text = x.user_id);
UPDATE public.schedule_events x SET household_id = u.household_id FROM public.users u
    WHERE x.household_id IS NULL AND COALESCE(btrim(x.family_group), '') = ''
      AND COALESCE(x.user_id, '') <> '' AND u.household_id IS NOT NULL
      AND (u.platform_id = x.user_id OR u.id::text = x.user_id);
UPDATE public.topic_memories x SET household_id = u.household_id FROM public.users u
    WHERE x.household_id IS NULL AND COALESCE(btrim(x.family_group), '') = ''
      AND COALESCE(x.user_id, '') <> '' AND u.household_id IS NOT NULL
      AND (u.platform_id = x.user_id OR u.id::text = x.user_id);

-- 6. The holding household, only when a row is still without one.
INSERT INTO public.households (id, name, legacy_label, created_at, updated_at)
SELECT '00000000-0000-0000-0000-000000000000', 'Rows without a family (pre-V4)', NULL, now(), now()
WHERE EXISTS (
    SELECT 1 FROM public.users WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.pairing_codes WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.automation_rules WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.baby_care_logs WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.baby_inventory_items WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.baby_profiles WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.category_budgets WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.entity_profiles WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.external_api_connectors WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.financial_transactions WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.home_maintenance_items WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.house_contract_records WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.mutation_logs WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.personal_crm_contacts WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.recurring_bills WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.schedule_events WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.shared_spaces WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.shared_task_reminders WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.topic_memories WHERE household_id IS NULL
    UNION ALL
    SELECT 1 FROM public.vehicle_assets WHERE household_id IS NULL
);

-- 7. The rest go to the holding household; then every household_id is required, a foreign key
--    and indexed.

UPDATE public.users SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.users ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.users ADD CONSTRAINT fk_users_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_users_household_id ON public.users USING btree (household_id);

UPDATE public.pairing_codes SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.pairing_codes ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.pairing_codes ADD CONSTRAINT fk_pairing_codes_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_pairing_codes_household_id ON public.pairing_codes USING btree (household_id);

UPDATE public.automation_rules SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.automation_rules ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.automation_rules ADD CONSTRAINT fk_automation_rules_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_automation_rules_household_id ON public.automation_rules USING btree (household_id);

UPDATE public.baby_care_logs SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.baby_care_logs ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.baby_care_logs ADD CONSTRAINT fk_baby_care_logs_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_baby_care_logs_household_id ON public.baby_care_logs USING btree (household_id);

UPDATE public.baby_inventory_items SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.baby_inventory_items ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.baby_inventory_items ADD CONSTRAINT fk_baby_inventory_items_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_baby_inventory_items_household_id ON public.baby_inventory_items USING btree (household_id);

UPDATE public.baby_profiles SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.baby_profiles ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.baby_profiles ADD CONSTRAINT fk_baby_profiles_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_baby_profiles_household_id ON public.baby_profiles USING btree (household_id);

UPDATE public.category_budgets SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.category_budgets ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.category_budgets ADD CONSTRAINT fk_category_budgets_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_category_budgets_household_id ON public.category_budgets USING btree (household_id);

UPDATE public.entity_profiles SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.entity_profiles ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.entity_profiles ADD CONSTRAINT fk_entity_profiles_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_entity_profiles_household_id ON public.entity_profiles USING btree (household_id);

UPDATE public.external_api_connectors SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.external_api_connectors ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.external_api_connectors ADD CONSTRAINT fk_external_api_connectors_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_external_api_connectors_household_id ON public.external_api_connectors USING btree (household_id);

UPDATE public.financial_transactions SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.financial_transactions ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.financial_transactions ADD CONSTRAINT fk_financial_transactions_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_financial_transactions_household_id ON public.financial_transactions USING btree (household_id);

UPDATE public.home_maintenance_items SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.home_maintenance_items ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.home_maintenance_items ADD CONSTRAINT fk_home_maintenance_items_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_home_maintenance_items_household_id ON public.home_maintenance_items USING btree (household_id);

UPDATE public.house_contract_records SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.house_contract_records ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.house_contract_records ADD CONSTRAINT fk_house_contract_records_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_house_contract_records_household_id ON public.house_contract_records USING btree (household_id);

UPDATE public.mutation_logs SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.mutation_logs ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.mutation_logs ADD CONSTRAINT fk_mutation_logs_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_mutation_logs_household_id ON public.mutation_logs USING btree (household_id);

UPDATE public.personal_crm_contacts SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.personal_crm_contacts ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.personal_crm_contacts ADD CONSTRAINT fk_personal_crm_contacts_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_personal_crm_contacts_household_id ON public.personal_crm_contacts USING btree (household_id);

UPDATE public.recurring_bills SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.recurring_bills ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.recurring_bills ADD CONSTRAINT fk_recurring_bills_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_recurring_bills_household_id ON public.recurring_bills USING btree (household_id);

UPDATE public.schedule_events SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.schedule_events ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.schedule_events ADD CONSTRAINT fk_schedule_events_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_schedule_events_household_id ON public.schedule_events USING btree (household_id);

UPDATE public.shared_spaces SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.shared_spaces ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.shared_spaces ADD CONSTRAINT fk_shared_spaces_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_shared_spaces_household_id ON public.shared_spaces USING btree (household_id);

UPDATE public.shared_task_reminders SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.shared_task_reminders ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.shared_task_reminders ADD CONSTRAINT fk_shared_task_reminders_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_shared_task_reminders_household_id ON public.shared_task_reminders USING btree (household_id);

UPDATE public.topic_memories SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.topic_memories ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.topic_memories ADD CONSTRAINT fk_topic_memories_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_topic_memories_household_id ON public.topic_memories USING btree (household_id);

UPDATE public.vehicle_assets SET household_id = '00000000-0000-0000-0000-000000000000' WHERE household_id IS NULL;
ALTER TABLE public.vehicle_assets ALTER COLUMN household_id SET NOT NULL;
ALTER TABLE ONLY public.vehicle_assets ADD CONSTRAINT fk_vehicle_assets_household FOREIGN KEY (household_id) REFERENCES public.households(id);
CREATE INDEX idx_vehicle_assets_household_id ON public.vehicle_assets USING btree (household_id);

-- 8. The composite indexes the hot tenant queries use, keyed by household as well as by label.
CREATE INDEX idx_mutation_logs_household_created ON public.mutation_logs USING btree (household_id, created_at DESC);
CREATE INDEX idx_financial_transactions_household_date ON public.financial_transactions USING btree (household_id, transaction_date DESC);
CREATE INDEX idx_baby_care_logs_household_timestamp ON public.baby_care_logs USING btree (household_id, "timestamp" DESC);
CREATE INDEX idx_schedule_events_household_start ON public.schedule_events USING btree (household_id, start_time);
CREATE INDEX idx_topic_memories_household_created ON public.topic_memories USING btree (household_id, created_at DESC);
CREATE INDEX idx_shared_task_reminders_household_completed_due ON public.shared_task_reminders USING btree (household_id, is_completed, due_time);

-- +goose Down
ALTER TABLE public.vehicle_assets DROP COLUMN household_id;
ALTER TABLE public.topic_memories DROP COLUMN household_id;
ALTER TABLE public.shared_task_reminders DROP COLUMN household_id;
ALTER TABLE public.shared_spaces DROP COLUMN household_id;
ALTER TABLE public.schedule_events DROP COLUMN household_id;
ALTER TABLE public.recurring_bills DROP COLUMN household_id;
ALTER TABLE public.personal_crm_contacts DROP COLUMN household_id;
ALTER TABLE public.mutation_logs DROP COLUMN household_id;
ALTER TABLE public.house_contract_records DROP COLUMN household_id;
ALTER TABLE public.home_maintenance_items DROP COLUMN household_id;
ALTER TABLE public.financial_transactions DROP COLUMN household_id;
ALTER TABLE public.external_api_connectors DROP COLUMN household_id;
ALTER TABLE public.entity_profiles DROP COLUMN household_id;
ALTER TABLE public.category_budgets DROP COLUMN household_id;
ALTER TABLE public.baby_profiles DROP COLUMN household_id;
ALTER TABLE public.baby_inventory_items DROP COLUMN household_id;
ALTER TABLE public.baby_care_logs DROP COLUMN household_id;
ALTER TABLE public.automation_rules DROP COLUMN household_id;
ALTER TABLE public.pairing_codes DROP COLUMN household_id;
ALTER TABLE public.users DROP COLUMN household_id;
DROP TABLE public.households;
