-- Pinned embeddings (V4-03), additive only: one row recording which embedding model made the
-- stored memory vectors, and their size. Vectors from two models cannot be compared, so the app
-- refuses to start when EMBEDDING_MODEL names another model, and only `mypa embeddings reembed`
-- changes the row, in the same transaction that replaces every vector (docs/EMBEDDINGS.md).
--
-- Every install so far embedded with gemini-embedding-001 at 3,072 dimensions: the model name was
-- written into the LLM client, and the columns are vector(3072). The release before V4 does not
-- read the table.

-- +goose Up
CREATE TABLE public.embedding_space (
    id bigint NOT NULL,
    model text NOT NULL,
    dimensions bigint NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT embedding_space_one_row CHECK (id = 1)
);
ALTER TABLE ONLY public.embedding_space ADD CONSTRAINT embedding_space_pkey PRIMARY KEY (id);
INSERT INTO public.embedding_space (id, model, dimensions, updated_at)
VALUES (1, 'gemini-embedding-001', 3072, now());

-- +goose Down
DROP TABLE public.embedding_space;
