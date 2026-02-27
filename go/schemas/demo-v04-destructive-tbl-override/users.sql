-- Fewer columns than v02: passwordhash removed (destructive)
CREATE TABLE public.users (
    id integer,
    username character varying(255)
    -- removed: passwordhash character varying(64)
);
