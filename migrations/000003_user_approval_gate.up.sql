-- Registration gatekeeping: new accounts start "pending" and must be
-- approved by an admin before they can log in or call any authenticated
-- endpoint. Existing rows default to 'approved' so nobody who already had
-- access gets locked out when this migration runs.

ALTER TABLE public.app_users
    ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'approved';

ALTER TABLE public.app_users DROP CONSTRAINT IF EXISTS app_users_status_check;
ALTER TABLE public.app_users
    ADD CONSTRAINT app_users_status_check
        CHECK (status IN ('pending', 'approved', 'rejected'));

CREATE INDEX IF NOT EXISTS idx_app_users_status ON public.app_users (status);