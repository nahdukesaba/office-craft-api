-- Audit trail for booking status transitions. Combined with
-- booking_proofs (already tracked with its own created_at/uploaded_by) at
-- the API layer to produce a single merged timeline per booking - no need
-- to duplicate proof rows in here.

CREATE TABLE IF NOT EXISTS public.booking_events (
                                                     id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id    uuid NOT NULL REFERENCES public.bookings (id) ON DELETE CASCADE,
    event_type    text NOT NULL, -- created, approved, auto_rejected, rejected, started, finished, cancelled, revoked
    from_status   text,
    to_status     text NOT NULL,
    actor_id      uuid REFERENCES public.app_users (id) ON DELETE SET NULL,
    notes         text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
    );

CREATE INDEX IF NOT EXISTS idx_booking_events_booking_id ON public.booking_events (booking_id, created_at);

-- Backfill a "created" event for any bookings that already existed before
-- this migration, so their timeline isn't empty going forward.
INSERT INTO public.booking_events (booking_id, event_type, from_status, to_status, actor_id, notes, created_at)
SELECT id, 'created', NULL, 'pending', user_id, 'Backfilled by migration 000004', created_at
FROM public.bookings
WHERE NOT EXISTS (
    SELECT 1 FROM public.booking_events e WHERE e.booking_id = public.bookings.id
);