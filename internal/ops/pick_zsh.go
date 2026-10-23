package ops

// zshScript is what pick --zsh prints (spec pick, --zsh): it binds Alt-K
// to type the references picked at the cursor, then redraws the prompt
// and the line, which fzf drew over.
const zshScript = `# koan pick: Alt-K types the references picked at the cursor.
# eval "$(koan pick --zsh)" in .zshrc; bindkey '<key>' __koan_pick_widget binds another key.
__koan_pick_widget() {
  local picked
  if picked=$(koan pick); then
    LBUFFER+=$picked
  fi
  zle reset-prompt
}
zle -N __koan_pick_widget
bindkey -M emacs '\ek' __koan_pick_widget
bindkey -M viins '\ek' __koan_pick_widget
`
