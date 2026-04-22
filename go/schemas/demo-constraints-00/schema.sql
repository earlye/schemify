CREATE TABLE public.categories (
    id BIGSERIAL,
    name TEXT NOT NULL UNIQUE,
    PRIMARY KEY (id)
);

CREATE TABLE public.articles (
    id BIGSERIAL,
    title TEXT NOT NULL,
    contents TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id)
);

CREATE TABLE public.questions (
    id            BIGSERIAL,
    question      TEXT NOT NULL,
    -- category      BIGINT NOT NULL REFERENCES public.categories (id),
    answer        TEXT NOT NULL,
    -- evidence_url  TEXT NOT NULL,
    article_id    BIGINT NOT NULL REFERENCES public.articles (id),
    difficulty    REAL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id)
);
