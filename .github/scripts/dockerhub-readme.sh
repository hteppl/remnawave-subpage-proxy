#!/usr/bin/env sh
set -eu

src="${1:-README.md}"
out="${2:-DOCKERHUB.md}"
repo_url="https://github.com/hteppl/remnawave-subpage-proxy"
limit=25000

LC_ALL=C awk -v repo="$repo_url" -v limit="$limit" '
function absolute(line,  rest, done, target) {
	rest = line
	done = ""
	while (match(rest, /\]\([^)]+\)/)) {
		target = substr(rest, RSTART + 2, RLENGTH - 3)
		if (target ~ /^#/) {
			target = repo target
		} else if (target !~ /^(https?|mailto):/) {
			target = repo "/blob/master/" target
		}
		done = done substr(rest, 1, RSTART - 1) "](" target ")"
		rest = substr(rest, RSTART + RLENGTH)
	}
	return done rest
}

{
	lines[NR] = absolute($0) "\n"
	total += length(lines[NR])
}

END {
	if (total <= limit) {
		for (i = 1; i <= NR; i++) {
			printf "%s", lines[i]
		}
		exit
	}

	footer = "\n---\n\nThe full README is on [GitHub](" repo "#readme).\n"
	budget = limit - length(footer) - length("```\n")
	for (i = 1; i <= NR && size + length(lines[i]) <= budget; i++) {
		printf "%s", lines[i]
		size += length(lines[i])
		if (lines[i] ~ /^```/) {
			fence = !fence
		}
	}
	if (fence) {
		printf "```\n"
	}
	printf "%s", footer
}
' "$src" >"$out"

size=$(wc -c <"$out" | tr -d " ")
echo "$out: $size bytes (limit $limit)"
