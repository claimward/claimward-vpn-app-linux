#!/usr/bin/env bash
# Negative control for the two UI gates, bricolint and mvvmlint.
#
# A green `go vet -vettool=...` proves only that a guard is SILENT on this tree,
# not that it can still BITE: a guard that resolves types wrongly, a stale
# binary or an analyzer that no-ops would pass CI while protecting nothing. So
# each guard is driven through the whole cycle on this app's own code:
#
#   1. clean tree               -> the guard passes
#   2. inject the leak it exists to catch, in internal/view/view.go
#                               -> the guard fails, AND plain `go vet` passes
#                                  (a leak that does not compile proves nothing)
#   3. remove the injection     -> the guard passes again
#
# The leaks:
#   bricolint  a raw p.FillRect on the *painter.PixelPainter Frame paints with
#   mvvmlint   a direct write to a DropDown's Options, outside any binding
#
# Usage: BRICOLINT=/path/to/bricolint MVVMLINT=/path/to/mvvmlint bash hack/lint-negative-control.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"
gopath="$(go env GOPATH)"
bricolint="${BRICOLINT:-$gopath/bin/bricolint}"
mvvmlint="${MVVMLINT:-$gopath/bin/mvvmlint}"
target="$root/internal/view/view.go"
marker='//lint-negative-control-injection'

backup="$(mktemp)"
armed=0
restore() { [ "$armed" = 1 ] && [ -s "$backup" ] && cp "$backup" "$target"; return 0; }
trap 'restore; rm -f "$backup"' EXIT
cp "$target" "$backup"
armed=1

vet() { (cd "$root" && GOWORK=off go vet "$@" ./...) >/dev/null 2>&1; echo $?; }

# control NAME TOOL ANCHOR INJECTION
control() {
	local name="$1" tool="$2" anchor="$3" inject="$4" rc
	[ -x "$tool" ] || { echo "error: $name not found at $tool" >&2; exit 2; }
	grep -qF "$anchor" "$backup" || { echo "error: anchor for $name not found in $target" >&2; exit 2; }

	echo "==> $name 1/3 clean tree: must pass"
	rc=$(vet -vettool="$tool")
	[ "$rc" = 0 ] || { echo "FAIL: $name fails on the clean tree (exit $rc)" >&2; exit 1; }

	echo "==> $name 2/3 injected leak: must fail, and the code must still build"
	awk -v anchor="$anchor" -v inj="$inject $marker" '{ print } index($0, anchor) { print inj }' "$backup" >"$target"
	grep -qF "$marker" "$target" || { echo "FAIL: injection not applied" >&2; exit 1; }
	rc=$(vet)
	[ "$rc" = 0 ] || { echo "FAIL: the injected code does not compile or vet (exit $rc); the control would prove nothing" >&2; exit 1; }
	rc=$(vet -vettool="$tool")
	[ "$rc" != 0 ] || { echo "FAIL: $name stayed green with the leak injected: it does not bite" >&2; exit 1; }
	echo "    ok: $name fired (exit $rc)"

	echo "==> $name 3/3 injection removed: must pass again"
	cp "$backup" "$target"
	rc=$(vet -vettool="$tool")
	[ "$rc" = 0 ] || { echo "FAIL: $name did not return to green (exit $rc)" >&2; exit 1; }
}

control bricolint "$bricolint" \
	'p := painter.NewPixelPainter(v.buf, v.w, v.h)' \
	'	p.FillRect(painter.Rect{}, painter.RGBA{})'
control mvvmlint "$mvvmlint" \
	'l, vm, inv := &v.leaves, v.vm, v.invalidate' \
	'	l.tenants.Options = nil'

echo "PASS: both guards bite on a real leak in this app and are silent otherwise"
