
# Prompt indicator (opt-in): WT_PROMPT carries the tree's name
# while the cwd is inside a linked worktree, for use like
#   PROMPT='...${WT_PROMPT:+ ⌂$WT_PROMPT}...'   (with setopt prompt_subst)
# Pure zsh — a few stats and one builtin read, recomputed only on
# cd, never per prompt render (PLAN.md R14). Deliberately uncached:
# trees come and go, and a cache would pin a dead answer for the
# life of the shell.
_wt_prompt_update() {
  emulate -L zsh
  unset WT_PROMPT
  local dir="$PWD" line
  while [[ -n "$dir" && "$dir" != / ]]; do
    if [[ -f "$dir/.git" ]]; then
      # a .git *file* marks a linked worktree: its gitdir line
      # points into the repository's shared .git/worktrees/<name>
      IFS= read -r line < "$dir/.git" 2>/dev/null
      if [[ "$line" == gitdir:*/worktrees/* ]]; then
        export WT_PROMPT="${dir:t}"
      fi
      break
    elif [[ -e "$dir/.git" ]]; then
      break
    fi
    dir="${dir:h}"
  done
  return 0
}
builtin emulate zsh -c 'autoload -Uz add-zsh-hook'
add-zsh-hook chpwd _wt_prompt_update
_wt_prompt_update
