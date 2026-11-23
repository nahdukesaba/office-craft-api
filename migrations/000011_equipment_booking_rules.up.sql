-- Equipment joins room/car/bike as a bookable resource type.
ALTER TABLE public.resources DROP CONSTRAINT IF EXISTS resources_type_check;
ALTER TABLE public.resources
    ADD CONSTRAINT resources_type_check
        CHECK (type IN ('room', 'car', 'bike', 'equipment'));

INSERT INTO public.resources (type, name, description)
SELECT 'equipment', v.name, v.description
FROM (VALUES
    ('Proyektor', 'Proyektor untuk rapat dan presentasi'),
    ('Layar Proyektor', 'Layar untuk proyektor'),
    ('Kamera DSLR Sony', 'Kamera DSLR merek Sony'),
    ('Kamera DSLR Nikon', 'Kamera DSLR merek Nikon')
) AS v(name, description)
WHERE NOT EXISTS (
    SELECT 1 FROM public.resources r WHERE r.name = v.name AND r.deleted_at IS NULL
);

-- Single-row admin switches for the borrowing rules. Both start off because
-- existing open bookings would otherwise block assets and staff immediately.
CREATE TABLE IF NOT EXISTS public.booking_rules (
    id                        boolean PRIMARY KEY DEFAULT true CHECK (id),
    block_resource_open_usage boolean NOT NULL DEFAULT false,
    block_user_unfinished     boolean NOT NULL DEFAULT false,
    updated_by                uuid,
    updated_at                timestamptz NOT NULL DEFAULT now()
);

INSERT INTO public.booking_rules (id) VALUES (true) ON CONFLICT (id) DO NOTHING;

ALTER TABLE public.booking_rules ENABLE ROW LEVEL SECURITY;
