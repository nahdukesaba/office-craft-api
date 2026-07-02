DROP TRIGGER IF EXISTS trg_bookings_updated_at ON public.bookings;
DROP TRIGGER IF EXISTS trg_resources_updated_at ON public.resources;
DROP TRIGGER IF EXISTS trg_app_users_updated_at ON public.app_users;
DROP FUNCTION IF EXISTS public.set_updated_at();

DROP TABLE IF EXISTS public.booking_proofs;
DROP TABLE IF EXISTS public.bookings;
DROP TABLE IF EXISTS public.resources;
DROP TABLE IF EXISTS public.app_users;
