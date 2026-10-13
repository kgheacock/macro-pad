-- The page tree that macropadd's state-db poller reads. See task 0051.
-- Task 0052's event log reduces into these tables; the poller never writes them.

CREATE TABLE nodes (
    id           INTEGER PRIMARY KEY,
    parent_id    INTEGER REFERENCES nodes(id) ON DELETE CASCADE,
    position     INTEGER,
    channel      TEXT CHECK (channel IN ('LIVE','DEV','MESSAGES','GITHUB','AI','CREATE')),
    message      TEXT,
    status       TEXT CHECK (status IN ('RUNNING','DONE','ERROR','WARN')),
    children_rev INTEGER NOT NULL DEFAULT 0,
    UNIQUE(parent_id, position)
);

CREATE TABLE state (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    active_node_id INTEGER REFERENCES nodes(id)
);
