DROP INDEX IF EXISTS idx_bookings_resource_status_range;

ALTER TABLE public.bookings DROP COLUMN IF EXISTS admin_notes;

ALTER TABLE public.bookings DROP CONSTRAINT IF EXISTS bookings_status_check;

UPDATE public.bookings SET status = 'completed' WHERE status = 'finished';
UPDATE public.bookings SET status = 'rejected' WHERE status = 'in_use';

ALTER TABLE public.bookings
    ADD CONSTRAINT bookings_status_check
        CHECK (status IN ('pending', 'approved', 'rejected', 'completed', 'cancelled'));