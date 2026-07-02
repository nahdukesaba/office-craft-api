-- Office-Craft initial schema
-- Note: auth.users is managed by Supabase Auth (GoTrue). We keep a mirrored
-- profile table (app_users) in the public schema so we can store role and
-- fullName without touching the protected auth schema.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ============================================================
-- app_users: profile data linked 1:1 to auth.users by id
-- ============================================================
CREATE TABLE IF NOT EXISTS public.app_users (
    id            uuid PRIMARY KEY,
    email         text NOT NULL UNIQUE,
    full_name     text NOT NULL DEFAULT '',
    role          text NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- ============================================================
-- resources: single-table inheritance for room / car / bike
-- ============================================================
CREATE TABLE IF NOT EXISTS public.resources (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type           text NOT NULL CHECK (type IN ('room', 'car', 'bike')),
    name           text NOT NULL,
    description    text NOT NULL DEFAULT '',
    location       text NOT NULL DEFAULT '',
    photo_url      text,
    is_available   boolean NOT NULL DEFAULT true,

    -- room-specific
    capacity       integer,
    amenities      jsonb NOT NULL DEFAULT '[]'::jsonb,

    -- car / bike-specific
    license_plate  text,
    seats          integer,
    fuel_type      text,

    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_resources_type ON public.resources (type);
CREATE INDEX IF NOT EXISTS idx_resources_is_available ON public.resources (is_available);

-- ============================================================
-- bookings
-- ============================================================
CREATE TABLE IF NOT EXISTS public.bookings (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id   uuid NOT NULL REFERENCES public.resources (id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES public.app_users (id) ON DELETE CASCADE,
    start_time    timestamptz NOT NULL,
    end_time      timestamptz NOT NULL,
    status        text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'approved', 'rejected', 'completed', 'cancelled')),
    purpose       text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_booking_time_order CHECK (end_time > start_time)
);

CREATE INDEX IF NOT EXISTS idx_bookings_resource_id ON public.bookings (resource_id);
CREATE INDEX IF NOT EXISTS idx_bookings_user_id ON public.bookings (user_id);
CREATE INDEX IF NOT EXISTS idx_bookings_status ON public.bookings (status);
CREATE INDEX IF NOT EXISTS idx_bookings_time_range ON public.bookings (resource_id, start_time, end_time);

-- ============================================================
-- booking_proofs
-- ============================================================
CREATE TABLE IF NOT EXISTS public.booking_proofs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id    uuid NOT NULL REFERENCES public.bookings (id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('before', 'after')),
    path          text NOT NULL,
    uploaded_by   uuid NOT NULL REFERENCES public.app_users (id),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_booking_proofs_booking_id ON public.booking_proofs (booking_id);

-- ============================================================
-- updated_at triggers
-- ============================================================
CREATE OR REPLACE FUNCTION public.set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_app_users_updated_at ON public.app_users;
CREATE TRIGGER trg_app_users_updated_at
    BEFORE UPDATE ON public.app_users
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

DROP TRIGGER IF EXISTS trg_resources_updated_at ON public.resources;
CREATE TRIGGER trg_resources_updated_at
    BEFORE UPDATE ON public.resources
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

DROP TRIGGER IF EXISTS trg_bookings_updated_at ON public.bookings;
CREATE TRIGGER trg_bookings_updated_at
    BEFORE UPDATE ON public.bookings
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();
