#!/bin/sh
# Build the SqueezeDSP engine (foxLyrionDSP + foxAudioLib, Fox's source with our patches) into the
# plugin's Bin folder, with the settings of Fox's Build-All.bat: pure Go, jsonv2, stripped.
#
#   sh engine/build.sh                  Linux x64 only (the rig)
#   sh engine/build.sh all              the six platforms the plugin ships
#   sh engine/build.sh osx-arm64 ...    named platforms
#   OUT=/some/dir sh engine/build.sh    write <OUT>/<platform>/ instead of the plugin's Bin
#
# Go: $GO, else the PATH, else /usr/local/go/bin/go. GOTOOLCHAIN=local, so Go never fetches a
# toolchain; the modules the source needs come from the Go proxy as source, cached in build/go
# (build/ is in .git/info/exclude). -trimpath keeps this machine's paths out of the binary.
#
# Each engine is written under the name git stores it by (squeezeDSP for most, ledger ENGINE NAME
# CASE); a zip build copies it to the exact name Binary.pm asks for.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
bin="$repo/SqueezeDSP/Bin"
go=${GO:-$(command -v go 2>/dev/null || echo /usr/local/go/bin/go)}
[ -x "$go" ] || { echo "build.sh: no Go at $go (set GO=)" >&2; exit 1; }

# platform -> GOOS GOARCH GOARM folder name
spec() {
	case $1 in
		linux-x64)   echo "linux amd64 - publishLinux-x64 squeezeDSP" ;;
		linux-arm64) echo "linux arm64 - publishlinux-arm64 squeezeDSP" ;;
		linux-arm)   echo "linux arm 7 publishlinux-arm squeezeDSP" ;;
		osx-x64)     echo "darwin amd64 - publishOsx-x64 squeezeDSP" ;;
		osx-arm64)   echo "darwin arm64 - publishOSX-arm64 SqueezeDSP" ;;
		win64)       echo "windows amd64 - publishWin64 SqueezeDSP.exe" ;;
		*) return 1 ;;
	esac
}

[ $# -eq 0 ] && set -- linux-x64
[ "$*" = all ] && set -- linux-x64 linux-arm64 linux-arm osx-x64 osx-arm64 win64

cache="$repo/build/go"
mkdir -p "$cache"
export GOTOOLCHAIN=local CGO_ENABLED=0 GOEXPERIMENT=jsonv2 GOFLAGS=-modcacherw
export GOPATH="$cache/path" GOMODCACHE="$cache/mod" GOCACHE="$cache/build"

echo "$("$go" version); Fox's base: $(awk '{ printf "%s %s  ", $1, substr($2, 1, 7) }' "$here/UPSTREAM")"
for p in "$@"; do
	s=$(spec "$p") || { echo "build.sh: unknown platform $p" >&2; exit 1; }
	read -r goos goarch goarm dir name <<-END
	$s
	END
	dest="${OUT:-$bin}/$dir/$name"
	mkdir -p "$(dirname "$dest")"
	tmp="$cache/out.$$"
	[ "$goarm" = - ] && goarm=
	( cd "$here/foxLyrionDSP" && GOOS=$goos GOARCH=$goarch GOARM=$goarm \
		"$go" build -trimpath -buildvcs=false -ldflags="-s -w" -o "$tmp" foxLyrionDSP.go )
	# cat onto an existing file keeps its name's case on a case-insensitive disk
	cat "$tmp" > "$dest"
	rm -f "$tmp"
	echo "$p: $dest $(wc -c < "$dest" | tr -d ' ') bytes sha1 $(shasum "$dest" | cut -c1-40)"
done
