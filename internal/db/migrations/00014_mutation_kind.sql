-- BL-68, additive only. Every chat and dashboard change to an undoable row is logged as create,
-- update or delete in `action`, which is what undo reads; `kind` keeps the domain's own name for it
-- ("transaction_logged", "supersede"), which people, the partner notifier and household rules go
-- on seeing. `also_json` holds the other rows the change touched, as they were before it, which the
-- same undo puts back (the older memory a new one replaced). Entries written before keep their old
-- action and empty new columns; undo reads them as before, where their snapshot allows.

-- +goose Up
ALTER TABLE public.mutation_logs ADD COLUMN kind text;
ALTER TABLE public.mutation_logs ADD COLUMN also_json jsonb;

-- +goose Down
ALTER TABLE public.mutation_logs DROP COLUMN also_json;
ALTER TABLE public.mutation_logs DROP COLUMN kind;
