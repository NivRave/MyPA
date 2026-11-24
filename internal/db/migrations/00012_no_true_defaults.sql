-- BL-43, BL-62: these columns defaulted to true, and the model tags carried the same default. GORM
-- writes a tag default in place of a field's zero value, so every false written through the app
-- was stored as true. The tags no longer carry a default and the app writes each value; the
-- columns follow the models. This changes no row: a row stored true before this release cannot be
-- told apart from one meant as true (docs/RESTORE_AND_MIGRATE.md, upgrade notes).

-- +goose Up
ALTER TABLE public.financial_transactions ALTER COLUMN is_shared DROP DEFAULT;
ALTER TABLE public.recurring_bills ALTER COLUMN auto_renew DROP DEFAULT;
ALTER TABLE public.shared_spaces ALTER COLUMN can_view DROP DEFAULT;
ALTER TABLE public.shared_spaces ALTER COLUMN can_edit DROP DEFAULT;
ALTER TABLE public.automation_rules ALTER COLUMN is_active DROP DEFAULT;
ALTER TABLE public.schedule_events ALTER COLUMN sync_to_google DROP DEFAULT;
ALTER TABLE public.topic_calendar_configs ALTER COLUMN include_in_morning_briefing DROP DEFAULT;

-- +goose Down
ALTER TABLE public.topic_calendar_configs ALTER COLUMN include_in_morning_briefing SET DEFAULT true;
ALTER TABLE public.schedule_events ALTER COLUMN sync_to_google SET DEFAULT true;
ALTER TABLE public.automation_rules ALTER COLUMN is_active SET DEFAULT true;
ALTER TABLE public.shared_spaces ALTER COLUMN can_edit SET DEFAULT true;
ALTER TABLE public.shared_spaces ALTER COLUMN can_view SET DEFAULT true;
ALTER TABLE public.recurring_bills ALTER COLUMN auto_renew SET DEFAULT true;
ALTER TABLE public.financial_transactions ALTER COLUMN is_shared SET DEFAULT true;
