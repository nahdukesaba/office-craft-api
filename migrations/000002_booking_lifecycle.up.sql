-- Booking lifecycle expansion: pending -> approved -> in_use -> finished,
-- with rejected/cancelled as terminal off-ramps. Replaces the old single
-- "completed" status with "finished", and adds "in_use" for the
-- start/finish workflow. Also adds admin_notes for revoke/auto-reject audit
-- trails.

ALTER TABLE public.bookings DROP CONSTRAINT IF EXISTS bookings_status_check;

-- Migrate any existing 'completed' rows to 'finished' before tightening the
-- constraint back down.
UPDATE public.bookings SET status = 'finished' WHERE status = 'completed';

ALTER TABLE public.bookings
    ADD CONSTRAINT bookings_status_check
        CHECK (status IN ('pending', 'approved', 'in_use', 'finished', 'rejected', 'cancelled'));

ALTER TABLE public.bookings
    ADD COLUMN IF NOT EXISTS admin_notes text NOT NULL DEFAULT '';

-- Supports the overlap-conflict lookups on create/approve and the public
-- calendar/availability views, which all filter by resource + time range +
-- status.
CREATE INDEX IF NOT EXISTS idx_bookings_resource_status_range
    ON public.bookings (resource_id, status, start_time, end_time);