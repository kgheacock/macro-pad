// The only file that touches node:sqlite. It uses DatabaseSync, prepare,
// run, get, all, and exec, so an API change in Node stays in this file.

import { mkdirSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { StoreError } from './errors.js';

export const SCHEMA_VERSION = 2;
export const DEFAULT_STATUS = 'info';

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

export function openStore(file, env = process.env) {
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
  return new Store(db, file, limit);
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

  constructor(db, file, limitMs) {
    this.#db = db;
    this.#file = file;
    this.#limit = limitMs;
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

  // Returns the id of the new message.
  insert(channel, body, cwd, { status = DEFAULT_STATUS, now = Date.now() } = {}) {
    return this.#guard(() => {
      const result = this.#db
        .prepare(
          'INSERT INTO messages (channel, body, created_at, cwd, status) VALUES (?, ?, ?, ?, ?)',
        )
        .run(channel, body, now, cwd, status);
      return Number(result.lastInsertRowid);
    });
  }

  // Inserts every body in one transaction. Returns how many rows it added.
  insertMany(channel, bodies, cwd, { status = DEFAULT_STATUS, now = Date.now() } = {}) {
    return this.#guard(() =>
      this.#inTransaction(() => {
        const stmt = this.#db.prepare(
          'INSERT INTO messages (channel, body, created_at, cwd, status) VALUES (?, ?, ?, ?, ?)',
        );
        for (const body of bodies) stmt.run(channel, body, now, cwd, status);
        return bodies.length;
      }),
    );
  }

  // Messages oldest first. channel null means every channel. limit keeps the
  // newest N.
  list({ channel = null, includeSeen = false, limit = null } = {}) {
    return this.#guard(() => {
      const where = [];
      const params = [];
      if (channel !== null) {
        where.push('channel = ?');
        params.push(channel);
      }
      if (!includeSeen) where.push('seen_at IS NULL');
      const clause = where.length ? `WHERE ${where.join(' AND ')}` : '';
      const select = `SELECT id, channel, body, created_at, seen_at, cwd, status FROM messages ${clause}`;
      const sql =
        limit === null
          ? `${select} ORDER BY id`
          : `SELECT * FROM (${select} ORDER BY id DESC LIMIT ?) ORDER BY id`;
      if (limit !== null) params.push(limit);
      return this.#db.prepare(sql).all(...params).map((row) => ({ ...row }));
    });
  }

  // Per channel with unseen rows: the count, the newest unseen id, and its
  // created_at. Ordered by channel.
  unseen(channel = null) {
    return this.#guard(() => {
      const clause = channel === null ? '' : 'AND channel = ?';
      const rows = this.#db
        .prepare(
          `SELECT m.channel AS channel, c.count AS count, m.id AS newestId, m.created_at AS createdAt
             FROM messages m
             JOIN (SELECT channel, COUNT(*) AS count, MAX(id) AS newest
                     FROM messages WHERE seen_at IS NULL ${clause} GROUP BY channel) c
               ON m.id = c.newest
            ORDER BY m.channel`,
        )
        .all(...(channel === null ? [] : [channel]));
      return rows.map((row) => ({ ...row }));
    });
  }

  count(channel = null) {
    return this.unseen(channel).reduce((sum, row) => sum + row.count, 0);
  }

  // Marks unseen rows of channel with id <= upToId. Returns how many changed.
  markSeen(channel, upToId, now = Date.now()) {
    return this.#guard(() => {
      const result = this.#db
        .prepare(
          'UPDATE messages SET seen_at = ? WHERE channel = ? AND id <= ? AND seen_at IS NULL',
        )
        .run(now, channel, upToId);
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
