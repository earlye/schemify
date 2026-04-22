-- A simple map showing which team(s) own a given workflow.
-- For example, Johnny Smith may have written the x.com get_business workflow.
CREATE TABLE public.workflow_owners (
    workflow_name text NOT NULL,
    owner         text NOT NULL
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_owners_workflow_name ON public.workflow_owners (workflow_name);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_owners_owner ON public.workflow_owners (owner);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_owners_nodupes ON public.workflow_owners (workflow_name, owner);

-- A simple map showing which team(s) own a given workspace.
-- For example, Billy Smith might be our CSR for L'Oreal.
CREATE TABLE public.workspace_owners (
    workspace_name text NOT NULL,
    owner          text NOT NULL
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workspace_owners_workspace_name ON public.workspace_owners (workspace_name);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workspace_owners_owner ON public.workspace_owners (owner);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workspace_owners_nodupes ON public.workspace_owners (workspace_name, owner);

-- Latest Kafka message per (topic, eventId). id is the eventId so e.g. select * where id='id-from-anomaly' works.
CREATE TABLE public.kafka_message_index (
    id         text   NOT NULL,
    partition  integer NOT NULL,
    msg_offset bigint  NOT NULL,
    message    jsonb   NOT NULL,
    topic      text   NOT NULL,
    PRIMARY KEY (topic, id)
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_kafka_message_index_topic ON public.kafka_message_index (topic);

-- Maps owners (workflow/workspace owner identity) to delivery channels. owner '*' is the fallback when a given owner has no rows.
-- channel is a discriminated union: {"kind": "PD"|"DD"|"Slack"|"Fail", "value": { ... }}.
CREATE TABLE public.owners_channels (
    owner   text   NOT NULL,
    channel jsonb  NOT NULL,
    CONSTRAINT owners_channels_channel_kind_check CHECK (
        -- channel ? 'kind' AND channel ? 'value' AND (channel->>'kind') IN ('PD', 'DD', 'Slack', 'Fail')
        (channel->>'kind') IN ('PD', 'DD', 'Slack', 'Fail') AND
        (channel->'value') IS NOT NULL
    )
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_owners_channels_owner ON public.owners_channels (owner);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_owners_channels_owner_kind ON public.owners_channels (owner, (channel->>'kind'));

CREATE TABLE public.environment_overrides (
    envKey text NOT NULL,
    envValue text NOT NULL,
    PRIMARY KEY(envKey)
);
