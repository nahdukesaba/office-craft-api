-- Resources get a UI color (hex string, e.g. "#0EA5E9") for calendar/badge
-- rendering on the frontend. "seats" is dropped entirely per product
-- decision - it was only ever set on car/bike resources and wasn't used
-- for any booking logic.

ALTER TABLE public.resources ADD COLUMN IF NOT EXISTS color text NOT NULL DEFAULT '#64748B';
ALTER TABLE public.resources DROP COLUMN IF EXISTS seats;