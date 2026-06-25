CREATE TABLE public.things (
    id BIGSERIAL,
    name TEXT NOT NULL
    -- DRIFT cleanup1 DROP (
    --   legacy_code TEXT NOT NULL
    -- )
);

-- DRIFT cleanup1 DROP (
-- CREATE INDEX CONCURRENTLY idx_things_legacy_code ON public.things (legacy_code);
-- )
