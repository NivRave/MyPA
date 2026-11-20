# Embeddings

MyPA recalls memories by meaning: every saved memory is stored with an embedding, a vector of
3,072 numbers, and each turn embeds the incoming message and fetches the nearest memories. This
page explains what is pinned, what you can change freely, and how to move to another embedding
model (V4-03).

## What is pinned

- **The embedding model is its own setting**, `EMBEDDING_MODEL`, default `gemini-embedding-001`.
  It is never taken from the chat model (`GEMINI_MODEL`).
- **The key is `GEMINI_API_KEY`.** Until a second embedding provider is supported, every install
  needs it for embeddings, including one that chats through another provider or a local model.
- **The size is fixed in the schema**: `vector(3072)` columns and HNSW indexes on
  `halfvec(3072)` (`models.EmbeddingDimensions`). Every vector is checked against it.
- **The database records which model made the stored vectors**, in the one-row table
  `embedding_space` (migration `00009_embedding_space.sql`, which records `gemini-embedding-001`,
  the model every install used until now). Vectors from two models cannot be compared, so the app
  **refuses to start** while `EMBEDDING_MODEL` names a different model than the recorded one, and
  says what to do. An install that has no embedded memory yet takes the configured model as its
  own.

## Changing the chat model or provider

Nothing to do. The chat model does not touch the stored vectors or the recorded model: change
`GEMINI_MODEL` (or, after V4-02, the provider) and restart.

## Commands

Run them where the app runs, with the app stopped:

```
docker compose stop app
docker compose run --rm app embeddings status
docker compose run --rm app embeddings reembed            # every memory, with EMBEDDING_MODEL
docker compose run --rm app embeddings reembed --missing  # only memories without an embedding
docker compose start app
```

- `status` shows the recorded model and size, the configured model, the size the schema stores,
  and how many memories have no embedding. It changes nothing.
- `reembed` embeds every memory again with `EMBEDDING_MODEL` and records that model. It works in
  one transaction: if anything fails (a quota error, a vector of the wrong size), nothing is
  changed and the memories stay as they were. It costs one embedding request per 50 memories.
- `reembed --missing` embeds only the memories that have no embedding (saved while the embedding
  service failed, or with `save_topic_memory` before V4-06, BL-24), in the recorded model. It
  refuses when `EMBEDDING_MODEL` is not the recorded model.
- Both refuse while the app is consuming messages (the queue has a consumer). `--skip-stop-check`
  skips that check; the re-embed holds a write lock on the memories, so a running app would wait
  on every memory it saves until the re-embed ends.

## Moving to another model of the same size

1. Take a backup (`/backup`, or `docker compose --profile tools run --rm transfer export`).
2. Stop the app: `docker compose stop app`.
3. Set `EMBEDDING_MODEL` in `.env` to the new model.
4. `docker compose run --rm app embeddings reembed`.
5. `docker compose start app`. It starts, because the recorded model is now the configured one.

To go back, set `EMBEDDING_MODEL` to the old model and run `reembed` again, or restore the
backup.

## Moving to a model of another size

A different size needs a code change, a schema migration and a full re-embed, released together
as a new version.

1. **Code.** Change the size in each of these files, and nowhere else
   (`TestEmbeddingDimensions_WrittenOnlyWhereTheChecklistSays` fails if the number appears
   anywhere else in production code):

   <!-- size-checklist -->
   - `internal/models/embedding.go`: `EmbeddingDimensions`
   - `internal/models/memory_v2.go`: the `vector(N)` tag of `TopicMemory.Embedding`
   - `internal/models/models.go`: the `vector(N)` tag of the legacy `Memory.Embedding`
   - `internal/db/vector.go`: `halfvecCosineDistanceSQL`, which must match the index expression
   - `internal/db/migrations.go`: the two HNSW index statements (`ApplyCustomIndexes`)
   - `internal/db/migrations/00001_baseline.sql`: history, never edited
   - `internal/db/migrations/00009_embedding_space.sql`: history, never edited
   <!-- /size-checklist -->

2. **Migration.** Add the next versioned migration with this content, the size (768 in the
   example) replaced by the new one. It drops the old vectors, which cannot be converted, and
   rebuilds the indexes. If V4-07 has dropped the legacy `memories` table, leave out its three
   lines.

   <!-- size-change-migration -->
   ```sql
   -- +goose Up
   DROP INDEX IF EXISTS public.idx_topic_memories_embedding;
   ALTER TABLE public.topic_memories ALTER COLUMN embedding TYPE public.vector(768) USING array_fill(0, ARRAY[768])::public.vector(768);
   CREATE INDEX idx_topic_memories_embedding ON public.topic_memories USING hnsw (((embedding)::public.halfvec(768)) public.halfvec_cosine_ops);
   DROP INDEX IF EXISTS public.idx_memories_embedding;
   ALTER TABLE public.memories ALTER COLUMN embedding TYPE public.vector(768) USING array_fill(0, ARRAY[768])::public.vector(768);
   CREATE INDEX idx_memories_embedding ON public.memories USING hnsw (((embedding)::public.halfvec(768)) public.halfvec_cosine_ops);

   -- +goose Down
   -- Not reversible: the old vectors are gone. Roll back by restoring the backup taken before.
   ```

   `TestReembed_ToAnotherSizeAfterTheDocumentedMigration` runs exactly this block against a test
   database and then re-embeds it.

3. **Upgrade.** Take a backup, stop the app, set `EMBEDDING_MODEL`, run the new release's
   `migrate` step (`docker compose run --rm migrate`), then
   `docker compose run --rm app embeddings reembed`, then start the app. Until the re-embed has
   run every memory is without an embedding: it is still found by keyword search and when pinned,
   but not by meaning.

## Checking the path on a copy of real data (owner)

V4-03 is done when the re-embed has been run once on a copy of real data. Use the throwaway copy
of OP-12 (compose project `mypa-dryrun`, restored from the newest backup, the app never started):

```
docker compose -p mypa-dryrun run --rm --no-deps app embeddings status
docker compose -p mypa-dryrun run --rm --no-deps app embeddings reembed
docker compose -p mypa-dryrun run --rm --no-deps app embeddings status
```

The first `status` shows `gemini-embedding-001`, 3,072 dimensions, and how many memories have no
embedding (the size of BL-24 on the live data). `reembed` with the default model replaces every
vector in the same space and reports how many it embedded. The second `status` shows the same
model and no memory without an embedding. `--no-deps` keeps compose from starting anything the
copy does not have; postgres and rabbitmq must be running in the copy.
