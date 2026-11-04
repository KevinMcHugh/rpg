-- migrate:up

-- A game's key is a readable label for the command line (`server build
-- --game heist`), not its identity: that is its id. Games belong to
-- different people now, and two of them may both call a game "heist", so
-- the key stops being unique. Commands take an id or a key, and refuse a
-- key that more than one game has. Keys of things within a game (a flag's,
-- a character's) stay unique within it, because scripts name them.

DROP INDEX games_key;
CREATE INDEX games_key ON games (key) WHERE deleted_at IS NULL;

-- migrate:down

-- Fails while two live games share a key; rename one first.
DROP INDEX games_key;
CREATE UNIQUE INDEX games_key ON games (key) WHERE deleted_at IS NULL;
