#!/bin/sh
# Install the Claimward VPN app and its privileged helper.
#
#   CGO_ENABLED=0 go build -o bin/ ./cmd/...      # as yourself, first
#   sudo ./scripts/install.sh --server https://vpn.example.com
#
# It creates the "claimward" group and adds the person who ran sudo to it (the
# helper's socket is 0660 root:claimward), installs the two binaries, the
# helper's configuration (owned by root, mode 0644), its systemd unit, and the
# desktop entry and icon, then starts the helper.
#
# Options:
#   --server URL   the claimward-vpn-server the helper may enroll with; written
#                  into a NEW /etc/claimward/helper.json (an existing one is kept)
#   --user NAME    who to add to the claimward group (default: $SUDO_USER)
#   --bin DIR      where the built binaries are (default: ./bin)
#   --prefix DIR   install prefix (default: /usr/local)
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)
server=""
user="${SUDO_USER:-}"
bin="$here/bin"
prefix=/usr/local
group=claimward

die() { echo "install.sh: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
	--server) [ $# -ge 2 ] || die "--server needs a URL"; server=$2; shift 2 ;;
	--user) [ $# -ge 2 ] || die "--user needs a name"; user=$2; shift 2 ;;
	--bin) [ $# -ge 2 ] || die "--bin needs a directory"; bin=$2; shift 2 ;;
	--prefix) [ $# -ge 2 ] || die "--prefix needs a directory"; prefix=$2; shift 2 ;;
	-h|--help) sed -n '2,20p' "$0"; exit 0 ;;
	*) die "unknown argument: $1 (see --help)" ;;
	esac
done

[ "$(id -u)" -eq 0 ] || die "run it as root: sudo $0 $*"
for b in claimward-app claimward-helper; do
	[ -x "$bin/$b" ] || die "$bin/$b is missing: build first, as yourself: CGO_ENABLED=0 go build -o bin/ ./cmd/..."
done
case "$server" in
"" | https://* | http://*) ;;
*) die "--server must be an http(s) URL: $server" ;;
esac
case "$server" in
*'"'* | *\\*) die "--server must not contain quotes or backslashes" ;;
esac

# The group that may use the helper's socket, and the person in it.
if ! getent group "$group" >/dev/null; then
	groupadd --system "$group"
	echo "created group $group"
fi
if [ -n "$user" ] && [ "$user" != root ]; then
	id "$user" >/dev/null 2>&1 || die "no such user: $user"
	usermod -a -G "$group" "$user"
	echo "added $user to $group (log out and back in for it to apply)"
fi

# Binaries.
install -D -o root -g root -m 0755 "$bin/claimward-helper" "$prefix/sbin/claimward-helper"
install -D -o root -g root -m 0755 "$bin/claimward-app" "$prefix/bin/claimward-app"

# The helper's configuration: root's alone, or the helper refuses it.
install -d -o root -g root -m 0755 /etc/claimward
cfg=/etc/claimward/helper.json
if [ ! -e "$cfg" ]; then
	if [ -n "$server" ]; then
		printf '{\n  "servers": ["%s"],\n  "group": "%s",\n  "socket": "/var/run/claimward-helper.sock"\n}\n' "$server" "$group" >"$cfg"
	else
		cp "$here/deploy/helper.json" "$cfg"
		echo "WARNING: $cfg lists the example server; edit \"servers\" and run: systemctl restart claimward-helper"
	fi
else
	echo "keeping the existing $cfg"
fi
chown root:root "$cfg"
chmod 0644 "$cfg"

# The systemd unit, with the binary where this prefix put it.
sed "s#/usr/local/sbin/claimward-helper#$prefix/sbin/claimward-helper#" \
	"$here/deploy/claimward-helper.service" >/etc/systemd/system/claimward-helper.service
chmod 0644 /etc/systemd/system/claimward-helper.service

# The desktop entry and icon.
install -d "$prefix/share/applications"
sed "s#/usr/local/bin/claimward-app#$prefix/bin/claimward-app#" \
	"$here/deploy/claimward.desktop" >"$prefix/share/applications/claimward.desktop"
chmod 0644 "$prefix/share/applications/claimward.desktop"
install -D -m 0644 "$here/assets/claimward.svg" "$prefix/share/icons/hicolor/scalable/apps/claimward.svg"
command -v update-desktop-database >/dev/null && update-desktop-database -q "$prefix/share/applications" || true
command -v gtk-update-icon-cache >/dev/null && gtk-update-icon-cache -q -t "$prefix/share/icons/hicolor" || true

systemctl daemon-reload
systemctl enable claimward-helper.service >/dev/null
systemctl restart claimward-helper.service
systemctl is-active --quiet claimward-helper.service ||
	die "the helper did not start: journalctl -u claimward-helper"
echo "claimward-helper is running; start the app from your desktop's menu (Claimward VPN)"
