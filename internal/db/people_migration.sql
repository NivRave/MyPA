-- Migration B of ADR 0002 (V4-32), additive only: people, their channel accounts, their
-- membership of a household, and single-use access tokens (link codes for now).
--
-- Run by Go migration 4 (schema_migrations.go), which then adds person_id beside every person
-- column, fills it, installs the triggers that keep filling it (person_keys.go), and logs how many
-- person keys found no person. This file is embedded, not a goose file: it is migration 4's first
-- half and never runs on its own. It is frozen with migration 4; change the schema in a new one.
--
-- Every users row becomes a person with the same ID, one channel account and one membership of
-- the household V4-30 gave it. The account's channel is left empty here: no code outside the
-- channel adapters names a platform (V4-31), so the app fills it at start-up by asking the adapters
-- which of them recognizes the ID (db.ResolveAccountChannels, the rule the code used before
-- V4-32). Nothing is merged: two rows that are the same human stay two people until that human
-- links them (Decision 4).

CREATE TABLE public.people (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    is_operator boolean DEFAULT false NOT NULL,
    preferred_account_id uuid,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);
ALTER TABLE ONLY public.people ADD CONSTRAINT people_pkey PRIMARY KEY (id);

CREATE TABLE public.channel_accounts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    person_id uuid NOT NULL,
    channel text NOT NULL,
    external_id text NOT NULL,
    linked_at timestamp with time zone NOT NULL,
    last_inbound_at timestamp with time zone
);
ALTER TABLE ONLY public.channel_accounts ADD CONSTRAINT channel_accounts_pkey PRIMARY KEY (id);
CREATE INDEX idx_channel_accounts_person_id ON public.channel_accounts USING btree (person_id);
CREATE UNIQUE INDEX idx_channel_accounts_channel_external ON public.channel_accounts USING btree (channel, external_id);
ALTER TABLE ONLY public.channel_accounts ADD CONSTRAINT fk_channel_accounts_person
    FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE CASCADE;
ALTER TABLE ONLY public.people ADD CONSTRAINT fk_people_preferred_account
    FOREIGN KEY (preferred_account_id) REFERENCES public.channel_accounts(id) ON DELETE SET NULL;

CREATE TABLE public.memberships (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    person_id uuid NOT NULL,
    household_id uuid NOT NULL,
    role text,
    created_at timestamp with time zone
);
ALTER TABLE ONLY public.memberships ADD CONSTRAINT memberships_pkey PRIMARY KEY (id);
CREATE UNIQUE INDEX idx_memberships_person_id ON public.memberships USING btree (person_id);
CREATE INDEX idx_memberships_household_id ON public.memberships USING btree (household_id);
ALTER TABLE ONLY public.memberships ADD CONSTRAINT fk_memberships_person
    FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE CASCADE;
ALTER TABLE ONLY public.memberships ADD CONSTRAINT fk_memberships_household
    FOREIGN KEY (household_id) REFERENCES public.households(id);
ALTER TABLE ONLY public.memberships ADD CONSTRAINT chk_memberships_role
    CHECK (role IS NULL OR role IN ('owner', 'adult', 'child', 'guest'));

CREATE TABLE public.access_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    purpose text NOT NULL,
    token_hash text NOT NULL,
    household_id uuid NOT NULL,
    person_id uuid,
    role text,
    created_by uuid,
    issued_on_account_id uuid,
    claimed_by_channel text,
    claimed_by_external_id text,
    claimed_at timestamp with time zone,
    issuer_confirmed_at timestamp with time zone,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    created_at timestamp with time zone
);
ALTER TABLE ONLY public.access_tokens ADD CONSTRAINT access_tokens_pkey PRIMARY KEY (id);
CREATE UNIQUE INDEX idx_access_tokens_token_hash ON public.access_tokens USING btree (token_hash);
CREATE INDEX idx_access_tokens_household_id ON public.access_tokens USING btree (household_id);
CREATE INDEX idx_access_tokens_person_id ON public.access_tokens USING btree (person_id);
ALTER TABLE ONLY public.access_tokens ADD CONSTRAINT fk_access_tokens_household
    FOREIGN KEY (household_id) REFERENCES public.households(id);
ALTER TABLE ONLY public.access_tokens ADD CONSTRAINT fk_access_tokens_person
    FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE CASCADE;
ALTER TABLE ONLY public.access_tokens ADD CONSTRAINT fk_access_tokens_created_by
    FOREIGN KEY (created_by) REFERENCES public.people(id) ON DELETE SET NULL;
ALTER TABLE ONLY public.access_tokens ADD CONSTRAINT fk_access_tokens_issued_on
    FOREIGN KEY (issued_on_account_id) REFERENCES public.channel_accounts(id) ON DELETE CASCADE;
ALTER TABLE ONLY public.access_tokens ADD CONSTRAINT chk_access_tokens_purpose
    CHECK (purpose IN ('invite', 'link', 'signin'));

-- 1. A person per user, keeping the user's ID.
INSERT INTO public.people (id, display_name, created_at, updated_at)
SELECT id, COALESCE(btrim(name), ''), COALESCE(created_at, now()), now()
FROM public.users;

-- 2. One channel account per user; the channel is filled at start-up (see above).
INSERT INTO public.channel_accounts (person_id, channel, external_id, linked_at)
SELECT id, '', platform_id, COALESCE(created_at, now())
FROM public.users
WHERE COALESCE(btrim(platform_id), '') <> '';

UPDATE public.people p SET preferred_account_id = ca.id
FROM public.channel_accounts ca WHERE ca.person_id = p.id;

-- When each account last wrote, from the audit sessions: the assistant starts messages only on an
-- account the person has written from (as before V4-32, when the latest session chose the channel).
UPDATE public.channel_accounts ca SET last_inbound_at = s.last
FROM (SELECT user_id, max(start_time) AS last FROM public.audit_sessions GROUP BY user_id) s
WHERE s.user_id = ca.external_id;

-- 3. One membership per person: the household only; the role stays on users until V4-35.
INSERT INTO public.memberships (person_id, household_id, created_at)
SELECT id, household_id, COALESCE(created_at, now())
FROM public.users
WHERE household_id IS NOT NULL;
