-- Usage tracking (V4-04), additive only: one row per chat model call with the tokens it used,
-- for the person whose turn made it and the turn (its audit session ID). The daily caps sum these
-- rows; the dashboard shows them per person, day and turn. The release before V4 does not read
-- the table.
--
-- person_id is set to NULL when the person is removed, so the install's total for the day keeps
-- the call; a merge of two people moves the rows to the survivor (MergeOnLink).

-- +goose Up
CREATE TABLE public.model_usage (
    id bigserial NOT NULL,
    person_id uuid,
    turn_id text DEFAULT ''::text NOT NULL,
    provider text DEFAULT ''::text NOT NULL,
    model text DEFAULT ''::text NOT NULL,
    input_tokens bigint DEFAULT 0 NOT NULL,
    output_tokens bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone NOT NULL
);
ALTER TABLE ONLY public.model_usage ADD CONSTRAINT model_usage_pkey PRIMARY KEY (id);
CREATE INDEX idx_model_usage_person_created ON public.model_usage USING btree (person_id, created_at);
CREATE INDEX idx_model_usage_turn_id ON public.model_usage USING btree (turn_id);
CREATE INDEX idx_model_usage_created_at ON public.model_usage USING btree (created_at);
ALTER TABLE ONLY public.model_usage ADD CONSTRAINT fk_model_usage_person
    FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE SET NULL;

-- +goose Down
DROP TABLE public.model_usage;
