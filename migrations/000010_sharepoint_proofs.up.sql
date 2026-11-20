-- Booking proofs may live in SharePoint (Microsoft Graph drive items)
-- instead of Supabase Storage. Rows with drive_item_id set are SharePoint
-- proofs; path keeps the owner/booking/file layout for access checks.
ALTER TABLE public.booking_proofs
    ADD COLUMN IF NOT EXISTS drive_item_id text,
    ADD COLUMN IF NOT EXISTS web_url text;

-- Latest Microsoft Graph refresh token. Graph rotates it on every refresh,
-- so the newest one must be persisted. RLS with no policies keeps it away
-- from Supabase anon/authenticated API roles; only the backend reads it.
CREATE TABLE IF NOT EXISTS public.graph_tokens (
    id            text PRIMARY KEY,
    refresh_token text NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE public.graph_tokens ENABLE ROW LEVEL SECURITY;
