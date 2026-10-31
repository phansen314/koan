#!/usr/bin/env bash
# Sets koan up for Claude Code: the permission rules that let the agent run
# koan and jq without prompting, except `koan init`, which makes this
# machine's root, the deletes, which have no undo but a backup, and
# `koan repair`, which changes the tree on disk; those always ask. Claude Code
# gets the skill from the koan plugin; see the README.
#
#   scripts/install.sh                         # when claude is on PATH
#   scripts/install.sh --claude                # even when it isn't
#   scripts/install.sh --uninstall [--claude]
#
# The rules go in ${CLAUDE_CONFIG_DIR:-~/.claude}/settings.json.
#
# Safe to rerun. The settings file is backed up (to .bak.<timestamp>, a new
# one each time) before it changes, and only koan's rules are added or
# removed, along with the original koan's rule for `koan migrate`, which is
# gone. Runs under macOS's /bin/bash 3.2. Needs jq.
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd -P)
skill_src=$repo/claude/skills/koan

claude_dir=${CLAUDE_CONFIG_DIR:-$HOME/.claude}
claude_settings=$claude_dir/settings.json
claude_allow='["Bash(koan:*)", "Bash(jq:*)"]'
claude_ask='["Bash(koan init:*)", "Bash(koan delete:*)", "Bash(koan repair:*)"]'
# What the original koan's install.sh added that this koan has no use for,
# and the asks for the deletes before they were one command.
claude_old='["Bash(koan migrate:*)", "Bash(koan task delete:*)", "Bash(koan folder delete:*)"]'

usage() {
	echo "usage: $0 [--claude] [--uninstall]" >&2
	exit 2
}

mode=install
want_claude=
for arg in "$@"; do
	case $arg in
	--claude) want_claude=1 ;;
	--uninstall) mode=uninstall ;;
	*) usage ;;
	esac
done

command -v jq >/dev/null || { echo "install.sh needs jq on PATH" >&2; exit 1; }
if [[ $mode == install ]] && ! command -v koan >/dev/null; then
	echo "koan is not on PATH. Install it with:" >&2
	echo "  GOBIN=~/.local/bin go install github.com/phansen314/koan/cmd/koan@latest" >&2
	echo "with GOBIN a directory on PATH, then rerun this script." >&2
	exit 1
fi

changed=

# Claude Code when --claude says so, or else when claude is on PATH
# (installing) or its settings have something to clean up (uninstalling).
if [[ -z $want_claude ]]; then
	if [[ $mode == install ]]; then
		if command -v claude >/dev/null; then
			want_claude=1
		else
			echo "claude: skipped, no claude on PATH"
		fi
	else
		if [[ -e $claude_settings ]]; then
			want_claude=1
		else
			echo "claude: skipped, no $claude_settings"
		fi
	fi
fi
if [[ -z $want_claude ]]; then
	echo "Nothing to do."
	exit 0
fi

# is_our_link PATH: PATH is a symlink to this clone's skill.
is_our_link() {
	local target
	[[ -L $1 ]] || return 1
	target=$(cd "$1" 2>/dev/null && pwd -P) || return 1
	[[ $target == "$skill_src" ]]
}

# A skill in ~/.claude/skills would load a second time: Claude Code has it from
# the plugin. An earlier version of this script linked it there; anything else there is the user's, so stop before
# changing anything.
stray=
for p in "$HOME/.claude/skills/koan" ${CLAUDE_CONFIG_DIR:+"$CLAUDE_CONFIG_DIR/skills/koan"}; do
	if [[ -e $p || -L $p ]] && ! is_our_link "$p"; then
		echo "skill: $p exists and isn't a link to $skill_src." >&2
		echo "  It would load as a second koan skill, beside the plugin's." >&2
		stray=1
	fi
done
if [[ -n $stray ]]; then
	if [[ $mode == install ]]; then
		echo "Move it out of the way and rerun. Nothing was changed." >&2
		exit 1
	fi
	echo "  Left in place." >&2
fi
for p in "$HOME/.claude/skills/koan" ${CLAUDE_CONFIG_DIR:+"$CLAUDE_CONFIG_DIR/skills/koan"}; do
	if is_our_link "$p"; then
		rm "$p"
		changed=1
		echo "skill: removed the old link $p"
	fi
done

if [[ -n $want_claude && -e $claude_settings ]] && ! jq empty "$claude_settings" 2>/dev/null; then
	echo "claude: $claude_settings isn't valid JSON; fix it and rerun. Nothing was changed." >&2
	exit 1
fi

# write_json LABEL FILE JSON: backs FILE up if it exists, then replaces it.
# Each backup gets its own timestamp, so an install then an uninstall doesn't
# overwrite the original with the installed version.
write_json() {
	local bak n
	mkdir -p "$(dirname "$2")"
	if [[ -e $2 ]]; then
		bak=$2.bak.$(date +%Y%m%d-%H%M%S)
		# Two runs in the same second: don't overwrite the first's.
		if [[ -e $bak ]]; then
			n=2
			while [[ -e $bak.$n ]]; do n=$((n + 1)); done
			bak=$bak.$n
		fi
		cp -p "$2" "$bak"
		echo "$1: backed up to $bak"
	fi
	printf '%s\n' "$3" >"$2.tmp"
	mv "$2.tmp" "$2"
	changed=1
	echo "$1: updated $2"
}

if [[ -n $want_claude ]]; then
	if [[ $mode == install ]]; then
		# shellcheck disable=SC2016 # $allow, $ask and $old are jq variables
		filter='
			((.permissions.allow // []) - $old) as $a
			| ((.permissions.ask // []) - $old) as $k
			| .permissions.allow = ($a + ($allow - $a))
			| .permissions.ask = ($k + ($ask - $k))'
	else
		# shellcheck disable=SC2016
		filter='
			if .permissions then
				.permissions.allow = ((.permissions.allow // []) - $allow - $old)
				| .permissions.ask = ((.permissions.ask // []) - $ask - $old)
				| if .permissions.allow == [] then del(.permissions.allow) else . end
				| if .permissions.ask == [] then del(.permissions.ask) else . end
				| if .permissions == {} then del(.permissions) else . end
			else . end'
	fi
	if [[ -e $claude_settings ]]; then
		cur=$(<"$claude_settings")
	else
		cur='{}'
	fi
	new=$(jq --argjson allow "$claude_allow" --argjson ask "$claude_ask" --argjson old "$claude_old" "$filter" <<<"$cur")
	# Order doesn't matter to Claude Code's rules, so compare sorted.
	if [[ $(jq -S . <<<"$cur") == "$(jq -S . <<<"$new")" ]]; then
		echo "claude: $claude_settings already up to date"
	elif [[ $mode == uninstall && ! -e $claude_settings ]]; then
		:
	else
		write_json claude "$claude_settings" "$new"
	fi
fi

if [[ $mode == install ]]; then
	# The original koan printed its version without the v, as 0.3.0.
	version=$(koan version | jq -r '.result.version // empty')
	if [[ -n $version && $version != v* && $version != "(devel)" ]]; then
		echo
		echo "The koan on PATH, $(command -v koan), is the original koan ($version),"
		echo "which this skill doesn't describe. Install this one with:"
		echo "  GOBIN=~/.local/bin go install github.com/phansen314/koan/cmd/koan@latest"
	elif ! koan info | jq -e '.result.state == "usable"' >/dev/null; then
		echo
		if [[ -e $HOME/koan-root/@koan.json ]]; then
			echo "$HOME/koan-root is a root of the original koan, which this koan can't use."
			echo "Move it aside, then set up a new root (see the README):"
			echo "  mv ~/koan-root ~/koan-root.old && koan init"
		else
			echo "koan has no usable root yet. Set one up with:"
			echo "  koan init"
		fi
	fi
	if [[ -n $want_claude ]] && command -v claude >/dev/null &&
		! claude plugin list --json 2>/dev/null | jq -e 'any(.[]; .id == "koan@koan" and .enabled)' >/dev/null; then
		echo
		echo "The koan plugin, which gives Claude Code the skill, is not installed. Install it with:"
		echo "  claude plugin marketplace add phansen314/koan"
		echo "  claude plugin install koan@koan"
	fi
fi

[[ -n $changed ]] || echo "Nothing changed."
