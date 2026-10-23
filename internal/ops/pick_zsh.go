package ops

// zshScript is what pick --zsh prints (spec pick, --zsh): a widget that
// types the references picked at the cursor, then redraws the prompt and
// the line, which fzf drew over. It binds no key; the person does.
const zshScript = `# koan pick: a widget that types the references picked at the cursor.
# eval "$(koan pick --zsh)" in .zshrc, then bind a key to it, such as Alt-K:
#   bindkey '\ek' __koan_pick_widget
__koan_pick_widget() {
  local picked
  if picked=$(koan pick); then
    LBUFFER+=$picked
  fi
  zle reset-prompt
}
zle -N __koan_pick_widget
`
