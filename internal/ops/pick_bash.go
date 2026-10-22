package ops

// bashScript is what pick --bash prints (spec pick, --bash): it binds
// Alt-K to type the references picked at the cursor. READLINE_LINE and
// READLINE_POINT need bash 4.0 or later; on an older bash it binds
// nothing.
const bashScript = `# koan pick: Alt-K types the references picked at the cursor.
# eval "$(koan pick --bash)" in .bashrc; bind -x '"<key>": __koan_pick_widget' binds another key.
__koan_pick_widget() {
  local picked
  picked=$(koan pick) || return 0
  READLINE_LINE="${READLINE_LINE:0:READLINE_POINT}${picked}${READLINE_LINE:READLINE_POINT}"
  READLINE_POINT=$((READLINE_POINT + ${#picked}))
}
if [[ ${BASH_VERSINFO[0]} -ge 4 ]]; then
  bind -m emacs-standard -x '"\ek": __koan_pick_widget'
  bind -m vi-insert -x '"\ek": __koan_pick_widget'
fi
`
