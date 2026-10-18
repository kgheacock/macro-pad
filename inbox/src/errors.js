// A UsageError is the caller's mistake: exit code 2. A StoreError is a
// problem with the SQLite file: exit code 1.

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
