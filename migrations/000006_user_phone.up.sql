-- Optional phone number, needed to send WhatsApp notifications. Nullable:
-- existing users simply won't get WhatsApp messages (email still works)
-- until they set one. Stored as plain digits in international format
-- without a leading '+' (e.g. "6281234567890"), matching what the OpenWA
-- WhatsApp gateway expects for its chatId.

ALTER TABLE public.app_users ADD COLUMN IF NOT EXISTS phone text;