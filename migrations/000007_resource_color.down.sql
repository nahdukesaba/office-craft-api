ALTER TABLE public.resources ADD COLUMN IF NOT EXISTS seats integer;
ALTER TABLE public.resources DROP COLUMN IF EXISTS color;