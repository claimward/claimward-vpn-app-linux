#!/bin/sh
# Remove what scripts/install.sh installed.
#
#   sudo ./scripts/uninstall.sh            # keeps /etc/claimward and the group
#   sudo ./scripts/uninstall.sh --purge    # removes them too
#
# Each person's own configuration and session (~/.config/Claimward,
# ~/.config/claimward) are left alone.
set -eu

prefix=/usr/local
purge=0
while [ $# -gt 0 ]; do
	case "$1" in
	--purge) purge=1; shift ;;
	--prefix) [ $# -ge 2 ] || { echo "--prefix needs a directory" >&2; exit 1; }; prefix=$2; shift 2 ;;
	-h|--help) sed -n '2,9p' "$0"; exit 0 ;;
	*) echo "uninstall.sh: unknown argument: $1" >&2; exit 1 ;;
	esac
done
[ "$(id -u)" -eq 0 ] || { echo "run it as root: sudo $0" >&2; exit 1; }

# Stopping the helper takes the tunnel down.
if systemctl list-unit-files claimward-helper.service >/dev/null 2>&1; then
	systemctl disable --now claimward-helper.service 2>/dev/null || true
fi
rm -f /etc/systemd/system/claimward-helper.service
systemctl daemon-reload

rm -f "$prefix/sbin/claimward-helper" "$prefix/bin/claimward-app"
rm -f "$prefix/share/applications/claimward.desktop"
rm -f "$prefix/share/icons/hicolor/scalable/apps/claimward.svg"
rm -f /var/run/claimward-helper.sock

if [ "$purge" -eq 1 ]; then
	rm -rf /etc/claimward
	getent group claimward >/dev/null && groupdel claimward || true
	echo "removed /etc/claimward and the claimward group"
fi
echo "Claimward removed"
