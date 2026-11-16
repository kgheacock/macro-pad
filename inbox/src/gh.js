// Runs the GitHub CLI. Shared by every feed that asks GitHub (tasks 0076, 0089).

export const GH_TIMEOUT_MS = 30 * 1000;

// Runs gh with args. Resolves its standard output; rejects with an Error
// that says why. With partial, a GraphQL answer that holds data is resolved
// even when gh exits 1 for the errors beside it. execFile is node's, or a
// stub in a test.
export function runGh(gh, args, { env, execFile, timeoutMs = GH_TIMEOUT_MS, partial = false }) {
  return new Promise((resolve, reject) => {
    execFile(gh, args, { env, timeout: timeoutMs, maxBuffer: 16 * 1024 * 1024, shell: false }, (error, stdout, stderr) => {
      if (!error || (partial && error.code === 1 && /"data"\s*:\s*\{/.test(String(stdout)))) {
        resolve(String(stdout));
        return;
      }
      if (error.code === 'ENOENT') {
        reject(new Error(`no ${gh}; install the GitHub CLI or set INBOX_GH`));
      } else if (error.killed || error.signal === 'SIGTERM') {
        reject(new Error(`${gh} did not answer within ${timeoutMs / 1000} s`));
      } else {
        const detail = String(stderr ?? '').trim().split('\n')[0] || error.message;
        reject(new Error(`${gh} failed: ${detail}`));
      }
    });
  });
}
