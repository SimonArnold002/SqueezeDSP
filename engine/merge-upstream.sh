#!/bin/sh
# Fox's engine source, kept mergeable. engine/UPSTREAM names the commit of each of his repos that our
# copy started from; only source is copied (*.go, go.mod, go.sum, LICENSE, README.md), not his binaries
# and test media.
#
#   sh engine/merge-upstream.sh            merge each repo's newer commits into our copy, three-way, file by
#                                          file (git merge-file); conflicts are left marked in the file;
#                                          UPSTREAM moves to the merged commit
#   sh engine/merge-upstream.sh status     what Fox has committed since our base, nothing changed
#   sh engine/merge-upstream.sh diff [repo]   our changes against the base (one repo's, or both): what a
#                                          pull request to Fox would carry (in his repo: git apply -p2)
#
# It reads Fox's GitHub repos into a temporary clone (blobs fetched as needed); nothing of his is changed.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
mode=${1:-merge}
only=${2:-}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

ours() { case $1 in *.go|go.mod|go.sum|LICENSE|README.md) return 0 ;; esac; return 1; }

cp "$here/UPSTREAM" "$work/UPSTREAM.in"
while read -r name base url; do
	[ -n "$name" ] || continue
	[ -z "$only" ] || [ "$only" = "$name" ] || continue
	g="$work/$name.git"
	git clone --quiet --bare --filter=blob:none "$url" "$g"
	head=$(git -C "$g" rev-parse HEAD)
	dir="$here/$name"

	if [ "$mode" = diff ]; then
		mkdir -p "$work/base/$name" "$work/ours/$name"
		git -C "$g" ls-tree -r --name-only "$base" | while read -r f; do
			ours "$(basename "$f")" || continue
			mkdir -p "$work/base/$name/$(dirname "$f")"
			git -C "$g" show "$base:$f" > "$work/base/$name/$f"
		done
		( cd "$dir" && find . -type f ) | while read -r f; do
			f=${f#./}
			ours "$(basename "$f")" || continue
			mkdir -p "$work/ours/$name/$(dirname "$f")"
			cp "$dir/$f" "$work/ours/$name/$f"
		done
		( cd "$work" && diff -ruN "base/$name" "ours/$name" ) || true
		continue
	fi

	if [ "$head" = "$base" ]; then
		echo "$name: up to date with Fox ($(echo "$base" | cut -c1-7))"
		continue
	fi
	echo "$name: Fox has $(git -C "$g" rev-list --count "$base..$head") commit(s) since $(echo "$base" | cut -c1-7):"
	git -C "$g" log --format='  %h %ad %s' --date=short "$base..$head"
	changes=$(git -C "$g" diff --no-renames --name-status "$base" "$head")
	if [ "$mode" = status ]; then
		echo "$changes" | while read -r st f; do ours "$(basename "$f")" && echo "  $st $f"; done
		continue
	fi

	echo "$changes" | while read -r st f; do
		ours "$(basename "$f")" || continue
		t="$dir/$f"
		case $st in
			A)
				mkdir -p "$(dirname "$t")"
				git -C "$g" show "$head:$f" > "$work/new"
				if [ ! -e "$t" ]; then
					cp "$work/new" "$t"; echo "  added    $f"
				elif cmp -s "$work/new" "$t"; then
					:
				else
					: > "$work/empty"
					if git merge-file --diff3 -L ours -L base -L fox "$t" "$work/empty" "$work/new"; then
						echo "  merged   $f"
					else
						echo "  CONFLICT $f"; touch "$work/conflict"
					fi
				fi ;;
			D)
				[ -e "$t" ] || continue
				git -C "$g" show "$base:$f" > "$work/old"
				if cmp -s "$work/old" "$t"; then
					rm "$t"; echo "  deleted  $f"
				else
					echo "  KEPT     $f (deleted by Fox, changed by us)"; touch "$work/conflict"
				fi ;;
			*)
				git -C "$g" show "$base:$f" > "$work/old"
				git -C "$g" show "$head:$f" > "$work/new"
				if [ ! -e "$t" ]; then
					echo "  MISSING  $f (changed by Fox, not in our copy)"; touch "$work/conflict"
				elif git merge-file --diff3 -L ours -L base -L fox "$t" "$work/old" "$work/new"; then
					echo "  merged   $f"
				else
					echo "  CONFLICT $f"; touch "$work/conflict"
				fi ;;
		esac
	done
	# record the merge so the next run starts from Fox's head (keep line order)
	awk -v n="$name" -v h="$head" '$1 == n { $2 = h } { print }' "$here/UPSTREAM" > "$work/UPSTREAM.out"
	cp "$work/UPSTREAM.out" "$here/UPSTREAM"
	echo "$name: base is now $(echo "$head" | cut -c1-7)"
done < "$work/UPSTREAM.in"

if [ -e "$work/conflict" ]; then
	echo "Conflicts are marked in the files above (<<<<<<< ours / ||||||| base / >>>>>>> fox). Resolve, then build." >&2
	exit 1
fi
