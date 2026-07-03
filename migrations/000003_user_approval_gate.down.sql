DROP INDEX IF EXISTS idx_app_users_status;
ALTER TABLE public.app_users DROP CONSTRAINT IF EXISTS app_users_status_check;
ALTER TABLE public.app_users DROP COLUMN IF EXISTS status;