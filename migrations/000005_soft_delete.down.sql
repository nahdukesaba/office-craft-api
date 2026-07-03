DROP INDEX IF EXISTS idx_booking_events_active;
DROP INDEX IF EXISTS idx_booking_proofs_active;
DROP INDEX IF EXISTS idx_bookings_active;
DROP INDEX IF EXISTS idx_resources_active;
DROP INDEX IF EXISTS idx_app_users_active;

ALTER TABLE public.booking_events DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE public.booking_proofs DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE public.bookings       DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE public.resources      DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE public.app_users      DROP COLUMN IF EXISTS deleted_at;