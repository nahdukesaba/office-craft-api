-- Downgrade closed/needs_revision bookings back to finished before
-- tightening the constraint, so the down migration doesn't leave orphaned
-- rows with a status value the constraint no longer allows.
UPDATE public.bookings SET status = 'finished' WHERE status IN ('closed', 'needs_revision');

ALTER TABLE public.bookings DROP CONSTRAINT IF EXISTS bookings_status_check;
ALTER TABLE public.bookings
    ADD CONSTRAINT bookings_status_check
        CHECK (status IN ('pending', 'approved', 'in_use', 'finished', 'rejected', 'cancelled'));