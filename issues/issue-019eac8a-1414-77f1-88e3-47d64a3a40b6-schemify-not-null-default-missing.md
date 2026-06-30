# Issue: schemify does not apply NOT NULL or DEFAULT when adding columns

**Symptom:** Adding a column defined as `TEXT NOT NULL DEFAULT 'init'` to an existing
table via `schemas/tasks.sql` caused schemify to generate an `ALTER TABLE` that added
the column as nullable with no default. Existing rows received NULL; the NOT NULL
constraint and default value were silently dropped from the migration.

**Workaround applied (2026-06-09):** Manually set all existing NULLs and ran
`ALTER TABLE tasks.task ALTER COLUMN state SET NOT NULL` and
`ALTER TABLE tasks.task ALTER COLUMN state SET DEFAULT 'init'` by hand.

**Impact:** Any schema change that adds a NOT NULL column with a default to an existing
table will require manual intervention until this is fixed in schemify.

**Action needed:** File a bug against schemify. The generated ALTER TABLE should
include `SET DEFAULT` and `SET NOT NULL` steps (with a backfill UPDATE for existing rows
if needed) when the target schema declares both constraints on a new column.
