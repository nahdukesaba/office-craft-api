DROP TABLE IF EXISTS public.booking_rules;

-- NOT VALID keeps equipment rows and their booking history in place.
ALTER TABLE public.resources DROP CONSTRAINT IF EXISTS resources_type_check;
ALTER TABLE public.resources
    ADD CONSTRAINT resources_type_check
        CHECK (type IN ('room', 'car', 'bike')) NOT VALID;
