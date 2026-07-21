-- Admin proof-review sign-off: from "finished", an admin either closes the
-- booking (final approval) or requests a revision if the after-photo
-- proof is insufficient, sending it to "needs_revision" until the user
-- uploads better photos and resubmits via /finish.

ALTER TABLE public.bookings DROP CONSTRAINT IF EXISTS bookings_status_check;
ALTER TABLE public.bookings
    ADD CONSTRAINT bookings_status_check
        CHECK (status IN ('pending', 'approved', 'in_use', 'finished', 'needs_revision', 'closed', 'rejected', 'cancelled'));