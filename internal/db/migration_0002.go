package db

// migration2SQL is goose migration 2: it copied the v1 `memories` rows into `topic_memories`
// (P2-4). It runs on the schema of version 2, before migration 3 adds household_id, so it must not
// name that column; migration 3 then maps the copied rows to households like every other row.
// Migration 11 copies any row still without its copy and drops the table (V4-07). No code path
// reads the table: this is the migration's history, kept because a migration never changes.
const migration2SQL = `
		INSERT INTO topic_memories
			(user_id, family_group, topic, subtopic, tags_json, fact, scope,
			 created_by_user_id, valid_from, is_pinned, embedding, created_at)
		SELECT
			m.user_id,
			COALESCE(u.family_group, ''),
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
		FROM memories m
		LEFT JOIN users u ON u.platform_id = m.user_id
		WHERE NOT EXISTS (
			SELECT 1 FROM topic_memories t
			WHERE t.user_id = m.user_id AND t.fact = m.fact
		)`
