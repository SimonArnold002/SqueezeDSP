#!/bin/sh
# The DSD encoder's spec bench (foxDSDEncoder spec_test.go): at DSD64-512 and for every modulator, the noise in
# band and out of band, where it rises, and the loop's stability, on the encoder's shipped path; with DSF files,
# what the modulator adds to real music too. Simon 2026-11-03: "can we have test bench kept so we dont have to
# rebuild one each time". Nothing is built into the engine; build.sh's environment (GOTOOLCHAIN=local, the module
# cache in build/go).
#
#   sh engine/encoder-spec.sh                          tones and stability -> build/encoder-spec.md
#   sh engine/encoder-spec.sh a.dsf [b.dsf ...]        and each file at its own rate
#   ALLRATES=1 sh engine/encoder-spec.sh a.dsf         each file at every DSD rate
#   MUSICONLY=1 sh engine/encoder-spec.sh a.dsf        the music half only (no tones, no stability)
#   START=60 SECS=10 OUT=/some/report.md WRITE=/some/dir sh engine/encoder-spec.sh a.dsf
#                                                      the section, the report, the DSF files written (to cmp with
#                                                      the engine's own output for the same section)
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
go=${GO:-$(command -v go 2>/dev/null || echo /usr/local/go/bin/go)}
[ -x "$go" ] || { echo "encoder-spec.sh: no Go at $go (set GO=)" >&2; exit 1; }
cache="$repo/build/go"
export GOTOOLCHAIN=local CGO_ENABLED=0 GOEXPERIMENT=jsonv2 GOFLAGS=-modcacherw
export GOPATH="$cache/path" GOMODCACHE="$cache/mod" GOCACHE="$cache/build"

out=${OUT:-$repo/build/encoder-spec.md}
mkdir -p "$(dirname "$out")"
music=""
for f in "$@"; do
	case $f in /*) ;; *) f="$PWD/$f" ;; esac
	[ -f "$f" ] || { echo "encoder-spec.sh: no file $f" >&2; exit 1; }
	music="${music:+$music,}$f"
done
set -- -spec -specout="$out" -specstart="${START:-30}" -specsecs="${SECS:-10}"
[ -n "$music" ] && set -- "$@" -specmusic="$music"
[ -n "${ALLRATES:-}" ] && set -- "$@" -specallrates
[ -n "${WRITE:-}" ] && { mkdir -p "$WRITE"; set -- "$@" -specwrite="$WRITE"; }

cd "$here/foxAudioLib"
run='TestSpec'
[ -n "${MUSICONLY:-}" ] && run='^TestSpecMusic$'
"$go" test ./foxAudioEncoder/foxDSDEncoder/ -run "$run" -count=1 -timeout 90m -v -args "$@"
echo "report: $out"
