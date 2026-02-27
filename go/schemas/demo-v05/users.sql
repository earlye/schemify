CREATE TABLE public.users (
    id integer,
    username character varying(255),
    passwordhash character varying(64)
);

CREATE INDEX CONCURRENTLY idx_users_username ON public.users (username);