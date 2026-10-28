CREATE TABLE public.silapet_sso_flows (
    state_hash bytea PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at timestamptz
);

CREATE INDEX idx_silapet_sso_flows_expiry
    ON public.silapet_sso_flows (expires_at)
    WHERE used_at IS NULL;
