-- Quiet hours (V4-05), additive only: a reminder can be marked urgent, so it is delivered even
-- during its recipient's quiet hours. Every other reminder due then waits until they end. Existing
-- reminders are not urgent, which is how they were all treated before: quiet hours were only text
-- in the prompt. The release before V4 does not read the column.

-- +goose Up
ALTER TABLE public.scheduled_reminders ADD COLUMN urgent boolean DEFAULT false NOT NULL;
ALTER TABLE public.shared_task_reminders ADD COLUMN urgent boolean DEFAULT false NOT NULL;

-- +goose Down
ALTER TABLE public.shared_task_reminders DROP COLUMN urgent;
ALTER TABLE public.scheduled_reminders DROP COLUMN urgent;
