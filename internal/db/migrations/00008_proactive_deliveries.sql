-- Proactive delivery (V4-33, ADR 0003 Decision 3), additive only: every message the assistant
-- starts itself (a reminder, a briefing, a notification) becomes a row with a state before it is
-- sent, so a message a channel refuses is held, moved or sent as a template instead of lost, and
-- its outcome is recorded. The release before V4 does not read the table.

-- +goose Up
CREATE TABLE public.proactive_deliveries (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    person_id uuid NOT NULL,
    category text NOT NULL,
    text text DEFAULT ''::text NOT NULL,
    template_key text DEFAULT ''::text NOT NULL,
    template_values text DEFAULT ''::text NOT NULL,
    state text NOT NULL,
    route text DEFAULT ''::text NOT NULL,
    channel text DEFAULT ''::text NOT NULL,
    external_id text DEFAULT ''::text NOT NULL,
    refs text DEFAULT ''::text NOT NULL,
    skip text DEFAULT ''::text NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    attempts bigint DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone,
    due_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    delivered_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);
ALTER TABLE ONLY public.proactive_deliveries ADD CONSTRAINT proactive_deliveries_pkey PRIMARY KEY (id);
CREATE INDEX idx_proactive_deliveries_person_id ON public.proactive_deliveries USING btree (person_id);
CREATE INDEX idx_proactive_deliveries_state ON public.proactive_deliveries USING btree (state);
ALTER TABLE ONLY public.proactive_deliveries ADD CONSTRAINT fk_proactive_deliveries_person
    FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE CASCADE;

-- +goose Down
DROP TABLE public.proactive_deliveries;
