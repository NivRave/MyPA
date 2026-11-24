-- Retire the two legacy tables (V4-07).
--
-- memories: the v1 memory table. Migration 2 copied its rows into topic_memories (P2-4), and no
-- code has read or written it since. OP-7 checked the live database on 2026-11-24: one row,
-- already copied. A backup restored from an older install may still hold a row without its copy,
-- so the copy runs once more here, as migration 2 did, before the drop: no restored row is lost.
--
-- workflows: one person's scheduled instructions. Each becomes a cron automation rule of the
-- person's household whose action is `prompt: <instruction>`, which the scheduler runs as a turn
-- for the rule's person, as it ran the workflow. Inactive workflows are copied inactive.
--
-- A row whose person has no users row goes to the holding household, as migration 3 maps one.
-- The rows' person_id is filled by the tables' person-key triggers (migration 4).

-- +goose Up

-- 1. The holding household, only when a row to copy has no household to go to.
INSERT INTO public.households (id, name, legacy_label, created_at, updated_at)
SELECT '00000000-0000-0000-0000-000000000000', 'Rows without a family (pre-V4)', NULL, now(), now()
WHERE EXISTS (
    SELECT 1 FROM public.memories m
    WHERE NOT EXISTS (SELECT 1 FROM public.users u WHERE u.platform_id = m.user_id)
      AND NOT EXISTS (SELECT 1 FROM public.topic_memories t WHERE t.user_id = m.user_id AND t.fact = m.fact)
    UNION ALL
    SELECT 1 FROM public.workflows w
    WHERE NOT EXISTS (SELECT 1 FROM public.users u WHERE u.platform_id = w.user_id OR u.id = w.person_id)
)
ON CONFLICT (id) DO NOTHING;

-- 2. Every memory not yet in topic_memories, as migration 2 copied them.
INSERT INTO public.topic_memories
    (user_id, family_group, household_id, topic, subtopic, tags_json, fact, scope,
     created_by_user_id, valid_from, is_pinned, embedding, created_at)
SELECT
    m.user_id,
    COALESCE(u.family_group, ''),
    COALESCE(u.household_id, '00000000-0000-0000-0000-000000000000'::uuid),
    CASE WHEN m.scope = 'family' THEN 'family' ELSE 'personal' END,
    'fact',
    '[]'::jsonb,
    m.fact,
    CASE WHEN m.scope = 'family' THEN 'family' ELSE 'personal' END,
    m.user_id,
    m.created_at,
    false,
    m.embedding,
    m.created_at
FROM public.memories m
LEFT JOIN LATERAL (
    SELECT x.family_group, x.household_id FROM public.users x
    WHERE x.platform_id = m.user_id ORDER BY x.id LIMIT 1
) u ON true
WHERE NOT EXISTS (
    SELECT 1 FROM public.topic_memories t WHERE t.user_id = m.user_id AND t.fact = m.fact
);

-- 3. Every workflow, as a cron rule of its person's household.
INSERT INTO public.automation_rules
    (user_id, household_id, family_group, name, trigger_type, trigger_topic, condition_json,
     action_instruction, is_active, created_at)
SELECT
    w.user_id,
    COALESCE(u.household_id, '00000000-0000-0000-0000-000000000000'::uuid),
    COALESCE(u.family_group, ''),
    w.name,
    'cron',
    '*',
    jsonb_build_object('cron', w.cron_expression),
    'prompt: ' || w.instruction,
    w.is_active,
    w.created_at
FROM public.workflows w
LEFT JOIN LATERAL (
    SELECT x.family_group, x.household_id FROM public.users x
    WHERE x.id = w.person_id OR x.platform_id = w.user_id
    ORDER BY (x.id = w.person_id) DESC, x.id LIMIT 1
) u ON true
ORDER BY w.id;

-- 4. The tables, and the person-key trigger functions that only they used.
DROP TABLE public.memories;
DROP TABLE public.workflows;
DROP FUNCTION IF EXISTS public.mypa_person_ids_memories();
DROP FUNCTION IF EXISTS public.mypa_person_ids_workflows();

-- +goose Down
-- Not reversible in place: the copies above cannot be told apart from rows written since. Roll
-- back by restoring the backup taken before the upgrade: `mypa transfer import FILE --replace
-- --no-migrate` with the previous release (V4-43, docs/RESTORE_AND_MIGRATE.md).
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'migration 11 (V4-07) cannot be undone; restore the backup taken before the upgrade';
END $$;
-- +goose StatementEnd
