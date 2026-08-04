# Tasks

One task is one Markdown file. The folder that holds the file is the status of
the task.

| Folder | Meaning |
|---|---|
| `backlog/` | The spec is written. Work has not started. |
| `ongoing/` | Work is in progress. |
| `complete/` | Every item in the definition of done is verified. |

## The file

The name is `NNNN-kebab-title.md`. The number never changes. The file moves
between the folders.

Each spec holds:

- the problem,
- three approaches with pros and cons,
- one decision and its cost,
- a definition of done that an outside reviewer can verify,
- metadata: the issue ID, the PR link, the branch, and the owner.

## Move a task

```bash
git mv tasks/backlog/0001-example.md tasks/ongoing/    # start
git mv tasks/ongoing/0001-example.md tasks/complete/   # finish
```

After each move, set the `status` and the `updated` fields in the frontmatter.

CAUTION: Do not move a task to `complete/` while one box in the definition of
done is empty. A reviewer who did not write the code must tick the boxes.

## Create a task

Run `/task-spec` in Claude Code, or copy the template from
`~/.claude/skills/task-spec/templates/spec-template.md`.
