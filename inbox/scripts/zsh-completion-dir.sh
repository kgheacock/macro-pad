#!/bin/sh
# Print the directory where oh-my-zsh looks for completion files, or print
# nothing when oh-my-zsh is not installed. install.sh and uninstall.sh use it.
#
# oh-my-zsh adds $ZSH_CACHE_DIR/completions to fpath before compinit runs.
# Ask an interactive zsh for it, because the value can be set in .zshrc and
# this script does not see the PATH or variables of that shell. Fall back to
# the default location.

command -v zsh >/dev/null 2>&1 || exit 0

dir=$(zsh -ic 'print -r -- "${ZSH_CACHE_DIR:+MARK:$ZSH_CACHE_DIR/completions}"' </dev/null 2>/dev/null | sed -n 's/^MARK://p' | tail -n 1)
if [ -z "$dir" ] && [ -d "${ZSH:-$HOME/.oh-my-zsh}" ]; then
  dir=${ZSH:-$HOME/.oh-my-zsh}/cache/completions
fi
[ -z "$dir" ] || printf '%s\n' "$dir"
