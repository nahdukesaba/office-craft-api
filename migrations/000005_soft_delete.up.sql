-- Soft delete everywhere: rows are never physically removed, only marked
-- with deleted_at. Every repository read filters `deleted_at IS NULL`; the
-- only current hard-delete (resources) now sets this column instead.
--
-- Note: existing ON DELETE CASCADE foreign keys (resources -> bookings ->
-- booking_proofs/booking_events) become effectively dormant once deletes
-- are soft - that's intentional, it's exactly what preserves history when
-- a resource is "deleted".

ALTER TABLE public.app_users      ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE public.resources      ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE public.bookings       ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE public.booking_proofs ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE public.booking_events ADD COLUMN IF NOT EXISTS deleted_at timestamptz;

-- Partial indexes: fast for the common case (only active rows), tiny
-- because so few rows will ever have deleted_at set.
CREATE INDEX IF NOT EXISTS idx_app_users_active      ON public.app_users (id)      WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_resources_active      ON public.resources (id)      WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_bookings_active       ON public.bookings (id)       WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_booking_proofs_active ON public.booking_proofs (id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_booking_events_active ON public.booking_events (id) WHERE deleted_at IS NULL;