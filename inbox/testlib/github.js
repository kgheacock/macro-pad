// A fake GitHub GraphQL API for the repos feed (task 0097): it answers the
// contributed-to query and the aliased counts query as `gh api graphql`
// prints them.

// The value of `-f NAME=VALUE` in gh args, or undefined.
export function flag(args, name) {
  for (let i = 0; i < args.length - 1; i++) {
    if (args[i] === '-f' && args[i + 1].startsWith(`${name}=`)) return args[i + 1].slice(name.length + 1);
  }
  return undefined;
}

export const isGraphql = (args) => args[0] === 'api' && args[1] === 'graphql';
export const isContributed = (args) => isGraphql(args) && /repositoriesContributedTo/.test(flag(args, 'query') ?? '');
export const isCounts = (args) => isGraphql(args) && /fragment R on Repository/.test(flag(args, 'query') ?? '');

// The names a counts query asks for, in alias order.
export function askedNames(args) {
  const names = [];
  for (let i = 0; flag(args, `n${i}`) !== undefined; i++) names.push(flag(args, `n${i}`));
  return names;
}

const node = (owner, name, { issues = 0, prs = 0, archived = false, fork = false, pushedAt = '2026-11-16T00:00:00Z' } = {}) => ({
  name,
  owner: { login: owner },
  url: `https://github.com/${owner}/${name}`,
  isFork: fork,
  isArchived: archived,
  pushedAt,
  issues: { totalCount: issues },
  pullRequests: { totalCount: prs },
});

// One page of the contributed-to answer. names: ['a', 'other/b', ...]; a name
// with no slash is of `owner`.
export function contributedPage(names, { owner = 'kgheacock', next = null, repos = {} } = {}) {
  const nodes = names.map((full) => {
    const [o, n] = full.includes('/') ? full.split('/') : [owner, full];
    const { issues, pullRequests, ...rest } = node(o, n, repos[n]);
    return rest;
  });
  return JSON.stringify({
    data: { viewer: { repositoriesContributedTo: { nodes, pageInfo: { hasNextPage: next !== null, endCursor: next } } } },
  });
}

// The answer of a counts query: each asked name of `repos` (name -> fields)
// gets its row; any other is missing, as GitHub answers it (data null and an
// error beside).
export function countsAnswer(args, repos = {}, { owner = 'kgheacock' } = {}) {
  const data = {};
  const errors = [];
  askedNames(args).forEach((name, i) => {
    const key = Object.keys(repos).find((k) => k.toLowerCase() === name.toLowerCase());
    if (key === undefined) {
      data[`r${i}`] = null;
      errors.push({ type: 'NOT_FOUND', path: [`r${i}`], message: `Could not resolve to a Repository with the name '${owner}/${name}'.` });
    } else {
      data[`r${i}`] = node(owner, key, repos[key]);
    }
  });
  return errors.length > 0 ? { stdout: JSON.stringify({ data, errors }), error: Object.assign(new Error('exit 1'), { code: 1 }) } : { stdout: JSON.stringify({ data }) };
}

// A GitHub with `contributed` (names) and `repos` (name -> fields; every
// contributed name exists with zero counts unless given). Answers a graphql
// call, or null for any other.
export function fakeGitHub({ contributed = [], repos = {}, owner = 'kgheacock' } = {}) {
  const all = { ...Object.fromEntries(contributed.filter((n) => !n.includes('/')).map((n) => [n, {}])), ...repos };
  return (args) => {
    if (isContributed(args)) return { stdout: contributedPage(contributed, { owner, repos: all }) };
    if (isCounts(args)) return countsAnswer(args, all, { owner });
    return null;
  };
}
