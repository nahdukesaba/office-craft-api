DROP TABLE IF EXISTS public.graph_tokens;

ALTER TABLE public.booking_proofs
    DROP COLUMN IF EXISTS web_url,
    DROP COLUMN IF EXISTS drive_item_id;
