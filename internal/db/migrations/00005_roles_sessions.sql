-- Migration C of ADR 0002 (V4-35), additive only: roles move from users.role to the membership and
-- the person, and dashboard sessions get a table.
--
-- admin becomes owner and operator; family (and any other value) becomes adult, the least that
-- every member before V4-35 had. A household with no admin gets no owner automatically: /doctor
-- reports it and the operator assigns one (`mypa access grant owner <person>`). users.role stays
-- and is still written ("admin" for an owner, "family" otherwise), so the release before V4 reads
-- what it always read; V4-44 drops it with users.
--
-- Pairing codes still outstanding keep working until they expire (24 hours at most), as an adult,
-- a child or a guest: a code never makes an owner (Decision 5).

-- +goose Up
UPDATE public.memberships m
SET role = CASE WHEN lower(btrim(u.role)) = 'admin' THEN 'owner' ELSE 'adult' END
FROM public.users u
WHERE u.id = m.person_id AND m.role IS NULL;

UPDATE public.memberships SET role = 'adult' WHERE role IS NULL;
ALTER TABLE public.memberships ALTER COLUMN role SET NOT NULL;

UPDATE public.people p SET is_operator = true
FROM public.users u
WHERE u.id = p.id AND lower(btrim(u.role)) = 'admin';

UPDATE public.pairing_codes
SET role = CASE lower(btrim(role)) WHEN 'child' THEN 'child' WHEN 'guest' THEN 'guest' ELSE 'adult' END;

CREATE TABLE public.sessions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    secret_hash text NOT NULL,
    person_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    last_seen_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    revoked_at timestamp with time zone,
    remote_address text DEFAULT ''::text NOT NULL
);
ALTER TABLE ONLY public.sessions ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);
CREATE UNIQUE INDEX idx_sessions_secret_hash ON public.sessions USING btree (secret_hash);
CREATE INDEX idx_sessions_person_id ON public.sessions USING btree (person_id);
ALTER TABLE ONLY public.sessions ADD CONSTRAINT fk_sessions_person
    FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE CASCADE;

-- +goose Down
DROP TABLE public.sessions;
ALTER TABLE public.memberships ALTER COLUMN role DROP NOT NULL;
