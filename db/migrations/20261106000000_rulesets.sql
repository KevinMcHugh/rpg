-- migrate:up

-- What the d20 rulesets need, and text imported under a license. See
-- docs/mechanics.md and docs/authorship.md.

-- The ability a skill adds in a d20 ruleset (srd52, srd35, pf2e).
ALTER TABLE skills ADD COLUMN ability TEXT CHECK (ability IN ('str', 'dex', 'con', 'int', 'wis', 'cha'));

-- Where a check goes on a critical success or failure (pf2e). Empty falls
-- back to the ordinary success or failure destination.
ALTER TABLE choices ADD COLUMN crit_success_node_id CHAR(20) REFERENCES nodes(id);
ALTER TABLE choices ADD COLUMN crit_fail_node_id CHAR(20) REFERENCES nodes(id);

-- Licensed text: written by a person, but not by anyone with a key here,
-- and brought in under a license (an SRD under CC-BY, the OGL or the ORC).
-- Such a revision names its source and the notice its license requires,
-- instead of a key. Only `server import` writes one; no API operation can.
ALTER TABLE text_revisions ADD COLUMN source TEXT;
ALTER TABLE text_revisions ADD COLUMN license_notice TEXT;
ALTER TABLE text_revisions ALTER COLUMN author_key DROP NOT NULL;
ALTER TABLE text_revisions ADD CONSTRAINT text_revisions_author_or_source
    CHECK ((author_key IS NOT NULL) <> (source IS NOT NULL)
       AND (source IS NULL) = (license_notice IS NULL));

CREATE OR REPLACE FUNCTION text_revisions_human_only() RETURNS trigger AS $$
BEGIN
    -- Licensed text names its source instead of a key.
    IF NEW.source IS NOT NULL THEN
        RETURN NEW;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM api_keys WHERE id = NEW.author_key AND kind = 'human') THEN
        RAISE EXCEPTION 'text revision by key % refused: only a human key may write player-facing text', NEW.author_key
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- migrate:down

CREATE OR REPLACE FUNCTION text_revisions_human_only() RETURNS trigger AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM api_keys WHERE id = NEW.author_key AND kind = 'human') THEN
        RAISE EXCEPTION 'text revision by key % refused: only a human key may write player-facing text', NEW.author_key
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
ALTER TABLE text_revisions DROP CONSTRAINT text_revisions_author_or_source;
DELETE FROM text_revisions WHERE source IS NOT NULL;
ALTER TABLE text_revisions ALTER COLUMN author_key SET NOT NULL;
ALTER TABLE text_revisions DROP COLUMN license_notice;
ALTER TABLE text_revisions DROP COLUMN source;
ALTER TABLE choices DROP COLUMN crit_fail_node_id;
ALTER TABLE choices DROP COLUMN crit_success_node_id;
ALTER TABLE skills DROP COLUMN ability;
