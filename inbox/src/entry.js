// The start of bin/inbox.js and bin/ai-inbox.js. It checks the Node version,
// hides the node:sqlite ExperimentalWarning, and hands the command line to
// src/cli.js with the profile of the command. It must not import anything
// that loads node:sqlite before the warning is hidden, so it imports late.

export async function entry(profileName) {
  const [major, minor] = process.versions.node.split('.').map(Number);
  if (major < 22 || (major === 22 && minor < 13)) {
    process.stderr.write(
      `${profileName}: needs Node 22.13 or newer; this is Node ${process.versions.node}\n`,
    );
    process.exit(1);
  }

  const emitWarning = process.emitWarning;
  process.emitWarning = function (warning, ...args) {
    const type = typeof args[0] === 'string' ? args[0] : args[0]?.type;
    if (type === 'ExperimentalWarning' && String(warning).includes('SQLite')) return;
    return emitWarning.call(this, warning, ...args);
  };

  const { main } = await import('./cli.js');
  const { PROFILES } = await import('./profiles.js');

  process.exitCode = await main(
    process.argv.slice(2),
    {
      get stdin() {
        return process.stdin;
      },
      stdout: process.stdout,
      stderr: process.stderr,
      env: process.env,
      cwd: () => process.cwd(),
      process,
    },
    PROFILES[profileName],
  );
}
