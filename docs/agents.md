# Setting up an agent by hand

`scripts/install.sh` does all of this for you. This page is for setting it up by hand, or for seeing what the script changes.

The rules let koan commands run without a prompt, except the ones that should ask:

- `init`, which changes this machine's setup;
- `task delete` and `folder delete`, which only git can undo;
- `repair`, which changes files to repair the tree;
- `migrate`, which rewrites them to a new format.

## What the script does

It needs `jq`. It backs up a settings file (to `.bak.<timestamp>`, a new one each time) before changing it, touches only koan's rules, and is safe to rerun. `--uninstall` takes it all out again.

For OpenCode it also links the skill into `~/.config/opencode/skills/koan`, so OpenCode's skill comes from your clone and `git pull` updates it. Claude Code's skill comes from the plugin: `claude plugin update koan@koan` picks up changes. To try an edited skill in Claude Code before pushing, run `claude --plugin-dir .` in a clone.

## Claude Code

In `~/.claude/settings.json`:

```json
{
  "permissions": {
    "allow": ["Bash(koan:*)", "Bash(jq:*)"],
    "ask": ["Bash(koan init:*)", "Bash(koan task delete:*)", "Bash(koan folder delete:*)", "Bash(koan repair:*)", "Bash(koan migrate:*)"]
  }
}
```

## OpenCode

In `~/.config/opencode/opencode.json`. The script leaves an `opencode.jsonc`, or a file with comments, alone, and prints these for you to add.

In OpenCode the last matching rule wins, so order matters: these go after any other rule that matches koan, and the asks after `"koan *"`.

```json
{
  "permission": {
    "bash": {
      "koan *": "allow",
      "jq *": "allow",
      "koan init*": "ask",
      "koan task delete *": "ask",
      "koan folder delete *": "ask",
      "koan repair*": "ask",
      "koan migrate*": "ask"
    }
  }
}
```

Link the skill from the clone:

```sh
ln -s "$PWD/claude/skills/koan" ~/.config/opencode/skills/koan
```

Not into `~/.claude/skills`: OpenCode reads that too, but so does Claude Code, which would then load the skill twice, next to the plugin's copy.

OpenCode also asks before its file tools touch anything outside the project, and that includes a task's notes file. To let it read and edit notes without asking, add `"external_directory": { "~/koan-root/*": "allow" }` under `"permission"`.
