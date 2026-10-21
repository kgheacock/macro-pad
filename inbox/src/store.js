// The only file that touches node:sqlite. It uses DatabaseSync, prepare,
// run, get, all, and exec, so an API change in Node stays in this file.

import { mkdirSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { StoreError } from './errors.js';

export const SCHEMA_VERSION = 4;
export const DEFAULT_STATUS = 'info';
// The plugin of a store opened without one, and of every row older than schema 4.
export const DEFAULT_PLUGIN = 'basic';

// MIGRATIONS[n] takes a file from user_version n to n + 1.
const MIGRATIONS = [
  `CREATE TABLE messages (
     id         INTEGER PRIMARY KEY AUTOINCREMENT,
     channel    TEXT    NOT NULL,
     body       TEXT    NOT NULL,
     created_at INTEGER NOT NULL,
     seen_at    INTEGER,
     cwd        TEXT    NOT NULL
   );
   CREATE INDEX messages_unseen ON messages (channel, id) WHERE seen_at IS NULL;`,
  `ALTER TABLE messages ADD COLUMN status TEXT NOT NULL DEFAULT '${DEFAULT_STATUS}';`,
  `ALTER TABLE messages ADD COLUMN here TEXT;
   CREATE TABLE channel_here (channel TEXT PRIMARY KEY, here TEXT NOT NULL);`,
  // Every row belongs to one plugin (plugins/index.js). Older rows are basic.
  `ALTER TABLE messages ADD COLUMN plugin TEXT NOT NULL DEFAULT 'basic';
   DROP INDEX messages_unseen;
   CREATE INDEX messages_unseen ON messages (plugin, channel, id) WHERE seen_at IS NULL;
   CREATE TABLE channel_here_new (
     plugin  TEXT NOT NULL,
     channel TEXT NOT NULL,
     here    TEXT NOT NULL,
     PRIMARY KEY (plugin, channel)
   );
   INSERT INTO channel_here_new (plugin, channel, here) SELECT 'basic', channel, here FROM channel_here;
   DROP TABLE channel_here;
   ALTER TABLE channel_here_new RENAME TO channel_here;`,
];

const DEFAULT_BUSY_TIMEOUT_MS = 5000;

export function defaultDbPath(env = process.env) {
  if (env.INBOX_DB) return env.INBOX_DB;
  const data = env.XDG_DATA_HOME || path.join(os.homedir(), '.local', 'share');
  return path.join(data, 'inbox', 'inbox.db');
}

function busyTimeoutMs(env) {
  const ms = Number(env.INBOX_BUSY_TIMEOUT_MS);
  return Number.isInteger(ms) && ms > 0 ? ms : DEFAULT_BUSY_TIMEOUT_MS;
}

function isBusy(e) {
  // SQLITE_BUSY is 5 and SQLITE_LOCKED is 6; extended codes keep them in the low byte.
  if (typeof e?.errcode === 'number') return [5, 6].includes(e.errcode & 0xff);
  return /database is locked|busy/i.test(e?.message ?? '');
}

function sleep(ms) {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

// Switching to WAL can fail with SQLITE_BUSY without waiting for the busy
// handler, so retry it within the same time limit.
function retryBusy(fn, limitMs) {
  const deadline = Date.now() + limitMs;
  for (;;) {
    try {
      return fn();
    } catch (e) {
      if (!isBusy(e) || Date.now() >= deadline) throw e;
      sleep(10);
    }
  }
}

function newerSchema(file, version) {
  return new StoreError(
    `inbox at ${file} has schema ${version}; this inbox reads up to ${SCHEMA_VERSION}. Update the inbox checkout.`,
  );
}

function userVersion(db) {
  return db.prepare('PRAGMA user_version').get().user_version;
}

function migrate(db, file) {
  if (userVersion(db) === SCHEMA_VERSION) return;
  db.exec('BEGIN IMMEDIATE');
  try {
    // Another process may have migrated while this one waited for the lock.
    const version = userVersion(db);
    if (version > SCHEMA_VERSION) throw newerSchema(file, version);
    for (let v = version; v < SCHEMA_VERSION; v++) db.exec(MIGRATIONS[v]);
    db.exec(`PRAGMA user_version = ${SCHEMA_VERSION}`);
    db.exec('COMMIT');
  } catch (e) {
    try {
      db.exec('ROLLBACK');
    } catch {
      // The transaction never started or has already ended.
    }
    throw e;
  }
}

// The store holds every plugin; the Store it returns reads and writes only
// the rows of `plugin`.
export function openStore(file, env = process.env, { plugin = DEFAULT_PLUGIN } = {}) {
  const limit = busyTimeoutMs(env);
  let db;
  try {
    mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
    db = new DatabaseSync(file);
    db.exec(`PRAGMA busy_timeout = ${limit}`);
    // Check the version before any pragma that could change the file.
    const version = userVersion(db);
    if (version > SCHEMA_VERSION) throw newerSchema(file, version);
    retryBusy(() => db.exec('PRAGMA journal_mode = WAL'), limit);
    retryBusy(() => migrate(db, file), limit);
  } catch (e) {
    db?.close();
    if (e instanceof StoreError) throw e;
    if (isBusy(e)) throw busyError(file, limit);
    throw new StoreError(
      `cannot open inbox at ${file}: ${e.message}. Set INBOX_DB or pass --db to use another file.`,
    );
  }
  return new Store(db, file, limit, plugin);
}

// "done,waiting,done" -> { done: 2, waiting: 1 }. A status holds no comma.
function countStatuses(list) {
  const counts = {};
  if (list === null) return counts;
  for (const status of list.split(',')) counts[status] = (counts[status] ?? 0) + 1;
  return counts;
}

function busyError(file, limitMs) {
  return new StoreError(
    `inbox at ${file} is busy (another writer held it for ${limitMs / 1000} s); try again`,
  );
}

export class Store {
  #db;
  #file;
  #limit;
  #plugin;

  constructor(db, file, limitMs, plugin) {
    this.#db = db;
    this.#file = file;
    this.#limit = limitMs;
    this.#plugin = plugin;
  }

  get plugin() {
    return this.#plugin;
  }

  #guard(fn) {
    try {
      return fn();
    } catch (e) {
      if (e instanceof StoreError) throw e;
      if (isBusy(e)) throw busyError(this.#file, this.#limit);
      throw new StoreError(`inbox at ${this.#file} failed: ${e.message}`);
    }
  }

  #inTransaction(fn) {
    this.#db.exec('BEGIN IMMEDIATE');
    try {
      const result = fn();
      this.#db.exec('COMMIT');
      return result;
    } catch (e) {
      try {
        this.#db.exec('ROLLBACK');
      } catch {
        // SQLite already rolled the transaction back.
      }
      throw e;
    }
  }

  // Returns the id of the new message. `here` is a "here" string (see
  // here.js) or null; this file stores it without looking at it.
  insert(channel, body, cwd, { status = DEFAULT_STATUS, here = null, now = Date.now() } = {}) {
    return this.#guard(() => {
      const result = this.#db
        .prepare(
          'INSERT INTO messages (plugin, channel, body, created_at, cwd, status, here) VALUES (?, ?, ?, ?, ?, ?, ?)',
        )
        .run(this.#plugin, channel, body, now, cwd, status, here);
      return Number(result.lastInsertRowid);
    });
  }

  // Inserts every body in one transaction. Returns how many rows it added.
  insertMany(channel, bodies, cwd, { status = DEFAULT_STATUS, here = null, now = Date.now() } = {}) {
    return this.#guard(() =>
      this.#inTransaction(() => {
        const stmt = this.#db.prepare(
          'INSERT INTO messages (plugin, channel, body, created_at, cwd, status, here) VALUES (?, ?, ?, ?, ?, ?, ?)',
        );
        for (const body of bodies) stmt.run(this.#plugin, channel, body, now, cwd, status, here);
        return bodies.length;
      }),
    );
  }

  // Messages oldest first. channel null means every channel. limit keeps the
  // newest N.
  list({ channel = null, includeSeen = false, limit = null } = {}) {
    return this.#guard(() => {
      const where = ['plugin = ?'];
      const params = [this.#plugin];
      if (channel !== null) {
        where.push('channel = ?');
        params.push(channel);
      }
      if (!includeSeen) where.push('seen_at IS NULL');
      const clause = `WHERE ${where.join(' AND ')}`;
      const select = `SELECT id, channel, body, created_at, seen_at, cwd, status, here FROM messages ${clause}`;
      const sql =
        limit === null
          ? `${select} ORDER BY id`
          : `SELECT * FROM (${select} ORDER BY id DESC LIMIT ?) ORDER BY id`;
      if (limit !== null) params.push(limit);
      return this.#db.prepare(sql).all(...params).map((row) => ({ ...row }));
    });
  }

  // Per channel with unseen rows: the count, the unseen count per status
  // (byStatus), the newest unseen id, its created_at, and its here (else the
  // channel's fallback, else null). Ordered by channel.
  unseen(channel = null) {
    return this.#guard(() => {
      const clause = channel === null ? '' : 'AND channel = ?';
      const rows = this.#db
        .prepare(
          `SELECT m.channel AS channel, c.count AS count, c.statuses AS statuses, m.id AS newestId,
                  m.created_at AS createdAt, COALESCE(m.here, h.here) AS here
             FROM messages m
             JOIN (SELECT channel, COUNT(*) AS count, MAX(id) AS newest, GROUP_CONCAT(status) AS statuses
                     FROM messages WHERE plugin = ? AND seen_at IS NULL ${clause} GROUP BY channel) c
               ON m.id = c.newest
             LEFT JOIN channel_here h ON h.plugin = m.plugin AND h.channel = m.channel
            ORDER BY m.channel`,
        )
        .all(this.#plugin, ...(channel === null ? [] : [channel]));
      return rows.map(({ statuses, ...row }) => ({ ...row, byStatus: countStatuses(statuses) }));
    });
  }

  // Per channel with a message (seen or not) created at or after `since`
  // (ms): the same fields as unseen(), except that newestId, createdAt, and
  // here are those of the newest message of any kind, and count and byStatus
  // are the unseen ones, so a channel with all messages seen has count 0 and
  // byStatus {}. Ordered by channel.
  recent(since) {
    return this.#guard(() => {
      const rows = this.#db
        .prepare(
          `SELECT m.channel AS channel, m.id AS newestId, m.created_at AS createdAt,
                  COALESCE(m.here, h.here) AS here,
                  (SELECT COUNT(*) FROM messages u
                    WHERE u.plugin = m.plugin AND u.channel = m.channel AND u.seen_at IS NULL) AS count,
                  (SELECT GROUP_CONCAT(u.status) FROM messages u
                    WHERE u.plugin = m.plugin AND u.channel = m.channel AND u.seen_at IS NULL) AS statuses
             FROM messages m
             JOIN (SELECT channel, MAX(id) AS newest
                     FROM messages WHERE plugin = ? AND created_at >= ? GROUP BY channel) n
               ON m.id = n.newest
             LEFT JOIN channel_here h ON h.plugin = m.plugin AND h.channel = m.channel
            ORDER BY m.channel`,
        )
        .all(this.#plugin, since);
      return rows.map(({ statuses, ...row }) => ({ ...row, byStatus: countStatuses(statuses) }));
    });
  }

  // Every channel that holds a message, seen or not, in name order.
  channels() {
    return this.#guard(() =>
      this.#db
        .prepare('SELECT DISTINCT channel FROM messages WHERE plugin = ? ORDER BY channel')
        .all(this.#plugin)
        .map((row) => row.channel),
    );
  }

  // The fallback "here" of a channel, or null.
  channelHere(channel) {
    return this.#guard(
      () => this.#db
          .prepare('SELECT here FROM channel_here WHERE plugin = ? AND channel = ?')
          .get(this.#plugin, channel)?.here ?? null,
    );
  }

  setChannelHere(channel, here) {
    this.#guard(() => {
      this.#db
        .prepare(
          'INSERT INTO channel_here (plugin, channel, here) VALUES (?, ?, ?) ON CONFLICT (plugin, channel) DO UPDATE SET here = excluded.here',
        )
        .run(this.#plugin, channel, here);
    });
  }

  // Returns true when the channel had a fallback.
  clearChannelHere(channel) {
    return this.#guard(
      () =>
        Number(
          this.#db
            .prepare('DELETE FROM channel_here WHERE plugin = ? AND channel = ?')
            .run(this.#plugin, channel).changes,
        ) > 0,
    );
  }

  // Changes when another connection commits to the file. Cheaper than a read
  // of the table, so a watcher polls this and reads the table on a change.
  dataVersion() {
    return this.#guard(() => this.#db.prepare('PRAGMA data_version').get().data_version);
  }

  count(channel = null) {
    return this.unseen(channel).reduce((sum, row) => sum + row.count, 0);
  }

  // Marks unseen rows of channel with id <= upToId. Returns how many changed.
  markSeen(channel, upToId, now = Date.now()) {
    return this.#guard(() => {
      const result = this.#db
        .prepare(
          'UPDATE messages SET seen_at = ? WHERE plugin = ? AND channel = ? AND id <= ? AND seen_at IS NULL',
        )
        .run(now, this.#plugin, channel, upToId);
      return Number(result.changes);
    });
  }

  // Marks what is unseen right now in channel (null: every channel). A
  // message that lands after the read stays unseen. Returns how many changed.
  markAllSeen(channel = null, now = Date.now()) {
    return this.#guard(() =>
      this.#inTransaction(() => {
        let changed = 0;
        for (const row of this.unseen(channel)) {
          changed += this.markSeen(row.channel, row.newestId, now);
        }
        return changed;
      }),
    );
  }

  close() {
    this.#db.close();
  }
}
