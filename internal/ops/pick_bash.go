package ops

// bashScript is what pick --bash prints (spec pick, --bash): a widget
// that types the references picked at the cursor. It binds no key; the
// person does. READLINE_LINE and READLINE_POINT need bash 4.0 or later;
// on an older bash it defines nothing.
const bashScript = `# koan pick: a widget that types the references picked at the cursor.
# eval "$(koan pick --bash)" in .bashrc, then bind a key to it, such as Alt-K:
#   bind -x '"\ek": __koan_pick_widget'
if [[ ${BASH_VERSINFO[0]} -ge 4 ]]; then
  __koan_pick_widget() {
    local picked
    picked=$(koan pick) || return 0
    READLINE_LINE="${READLINE_LINE:0:READLINE_POINT}${picked}${READLINE_LINE:READLINE_POINT}"
    READLINE_POINT=$((READLINE_POINT + ${#picked}))
  }
fi
`
