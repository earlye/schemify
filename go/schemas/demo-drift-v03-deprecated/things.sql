CREATE TABLE public.things (
    id BIGSERIAL,
    name TEXT NOT NULL,
    legacy_code TEXT NOT NULL
    -- DRIFT tolerate1 DEPRECATED (
    --   legacy_code TEXT NOT NULL
    -- )
);

CREATE INDEX CONCURRENTLY idx_things_legacy_code ON public.things (legacy_code);
