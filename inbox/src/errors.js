// A UsageError is the caller's mistake: exit code 2. A StoreError is a
// problem with the SQLite file: exit code 1. A DaemonRunningError means
// another daemon holds the store: exit code 3.

export class UsageError extends Error {
  constructor(message, { help = true } = {}) {
    super(message);
    this.name = 'UsageError';
    // help false: the message already says where to look, so skip the
    // "run 'inbox CMD -h' for help" line.
    this.help = help;
    this.command = undefined;
  }
}

export class StoreError extends Error {
  constructor(message) {
    super(message);
    this.name = 'StoreError';
  }
}

export class DaemonRunningError extends Error {
  constructor(message) {
    super(message);
    this.name = 'DaemonRunningError';
  }
}
