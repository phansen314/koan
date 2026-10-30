# Shell setup

Optional extras for using koan by hand: completion, and the `koan pick` picker bound to a key.

## Completion

For shell completion of commands, options and references (folder paths one folder at a time, IDs with their titles, and gates), load koan's script in your shell's startup file; `koan completion --help` lists the shells. bash needs the bash-completion package:

```shell
source <(koan completion bash)   # in ~/.bashrc; zsh: source <(koan completion zsh) in ~/.zshrc
```

## The picker

`koan pick` lets you choose tasks, gates and folders in fzf (0.63.0 or later) and prints their references. To have a key type the picked references in at the cursor, load pick's widget in your shell's startup file and bind it to a key. koan binds none for you, since which key is free depends on your terminal, keyboard and editing mode. bash needs 4.0 or later, so on macOS use Homebrew's bash rather than `/bin/bash`.

```shell
# ~/.zshrc
eval "$(koan pick --zsh)"
bindkey '\ek' __koan_pick_widget        # Alt-K

# ~/.bashrc
eval "$(koan pick --bash)"
bind -x '"\ek": __koan_pick_widget'     # Alt-K
```

koan paths are never globbed: a shell expands `/work/old/*` against your disk before koan runs, so it never means the folders under `/work/old`. Name each folder, completing it with Tab, or pick them with `$(koan pick)`.

### Choosing a key

- **Alt-K on Linux**, in the default emacs editing mode, works as above.
- **Alt-K on macOS** needs your terminal to send it first: see below.
- **In vi mode** (`bindkey -v`, `set -o vi`), Alt-K is the same keys as Esc then `k` typed quickly, which recalls the previous line, so bind a key in insert mode instead. Ctrl-K only inserts a literal `^K` there in both shells: `bindkey -M viins '^K' __koan_pick_widget` in zsh, or `bind -m vi-insert -x '"\C-k": __koan_pick_widget'` in bash.
- **Any other key** binds the same way: `bindkey '<key>' __koan_pick_widget`, or `bind -x '"<key>": __koan_pick_widget'`.

### Alt-K on macOS

On macOS, Option-K types a character, such as `˚` on a US keyboard, rather than sending Alt-K (escape then `k`), until your terminal is told otherwise. Either remap Option-K alone, which keeps every other Option key typing its character and works whatever your keyboard layout, or make the Option key a Meta key, which turns on your shell's other Alt keys too, such as Alt-B and Alt-F to move by word, but stops that key typing accented characters:

| Terminal | Option-K alone | Option as Meta |
|---|---|---|
| kitty | in `kitty.conf`: `map opt+k send_text all \x1bk` | in `kitty.conf`: `macos_option_as_alt left` (or `right`, or `both`) |
| iTerm2 | Settings → Profiles → Keys → Key Mappings → **+**: press Option-K, Action "Send Escape Sequence", Esc+ `k` | Settings → Profiles → Keys → General: Left Option key **Esc+** |
| Terminal.app | not possible: Terminal.app remaps only arrows, function and editing keys | Settings → Profiles → Keyboard: **Use Option as Meta key** (both Option keys) |

Or leave the terminal alone and bind the character your keyboard types for Option-K, which you can see by running `cat` and pressing it: `bindkey '˚' __koan_pick_widget` in zsh, or `bind -x '"˚": __koan_pick_widget'` in bash. You can no longer type that character, and it works only on that layout.
