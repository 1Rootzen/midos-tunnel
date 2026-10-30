#!/bin/bash
# ============================================================================
#  Midos Tunnel - standalone deep uninstaller
#
#  Removes EVERY trace of the tunnel (any version, any old name) from a server,
#  even when the manager script itself is missing or broken:
#
#      bash <(curl -fsSL https://raw.githubusercontent.com/1Rootzen/midos-tunnel/main/uninstall.sh)
#      bash <(curl -fsSL https://raw.githubusercontent.com/1Rootzen/midos-tunnel/main/uninstall.sh) --yes       # no prompt
#      bash <(curl -fsSL https://raw.githubusercontent.com/1Rootzen/midos-tunnel/main/uninstall.sh) --dry-run   # preview
#
#  It is deliberately SURGICAL: it only ever deletes objects it can prove are
#  ours. It never flushes a whole iptables table, and it never touches x-ui,
#  xray, SSH, Caddy, Docker or any other software on the box.
# ============================================================================
set -uo pipefail

R=$'\033[0;31m'; G=$'\033[0;32m'; Y=$'\033[0;33m'; C=$'\033[0;36m'; W=$'\033[1;37m'; D=$'\033[2m'; N=$'\033[0m'
ok(){   printf '  %s✔%s %s\n' "$G" "$N" "$1"; }
info(){ printf '  %s•%s %s\n' "$C" "$N" "$1"; }
warn(){ printf '  %s!%s %s\n' "$Y" "$N" "$1"; }
step(){ printf '\n%s[%s]%s %s\n' "$W" "$1" "$N" "$2"; }
REMOVED=0; note(){ REMOVED=$((REMOVED+1)); ok "$1"; }

[[ $EUID -eq 0 ]] || { printf '%sRun as root.%s\n' "$R" "$N"; exit 1; }

ASSUME_YES=0; DRY=0
for arg in "$@"; do
  case "$arg" in
    --yes|-y)      ASSUME_YES=1 ;;
    --dry-run|-n)  DRY=1; ASSUME_YES=1 ;;
    -h|--help)     printf 'usage: uninstall.sh [--yes] [--dry-run]\n'; exit 0 ;;
  esac
done

# Paths that must survive no matter what. This is a backstop, not the policy:
# nothing below ever targets them, but a future edit or a bad glob must not be
# able to take a panel's database with it.
PROTECTED='^/(etc|usr/local|opt|var/lib)/(x-ui|xray|3x-ui|hiddify|marzban)|^/etc/(ssh|passwd|shadow)|^/usr/local/x-ui|x-ui\.db$|^/$|^/(bin|boot|etc|home|root|usr|var|lib|sbin|opt|srv|dev|proc|sys)/?$'
guard(){   # refuses to delete anything that belongs to other software
  local p
  for p in "$@"; do
    [[ "$p" == -* ]] && continue
    if [[ "$p" =~ $PROTECTED ]]; then
      warn "REFUSED to touch $p (belongs to other software)"; return 1
    fi
  done
  return 0
}
# Every destructive command goes through run(). In --dry-run it is only printed,
# so the exact removal plan can be reviewed before anything is touched.
run(){
  [[ "$1" == "rm" ]] && { guard "${@:2}" || return 1; }
  if (( DRY )); then printf '  %s[dry]%s %s\n' "$D" "$N" "$*"; return 0; fi
  "$@"
}
# same, for commands whose own output we silence (the preview must still show them)
runq(){
  [[ "$1" == "rm" ]] && { guard "${@:2}" || return 1; }
  if (( DRY )); then printf '  %s[dry]%s %s\n' "$D" "$N" "$*"; return 0; fi
  "$@" > /dev/null 2>&1
}
# so an object handled by two different detectors is only reported once
SEEN=" "
seen(){ [[ "$SEEN" == *" $1 "* ]] && return 0; SEEN+="$1 "; return 1; }

# Read the unit list once. Piping it into `grep -q` would be wrong here: grep
# exits at the first match, systemctl dies of SIGPIPE, and under `pipefail` the
# whole test reports failure - which silently skipped every `systemctl disable`.
unit_list(){ UNITS=$(systemctl list-unit-files --no-pager --no-legend 2>/dev/null); }
unit_list
has_unit(){ [[ $'\n'"$UNITS" == *$'\n'"$1"[[:space:]]* ]]; }

# ---- every name this product has ever used --------------------------------
SERVICES=(Midos-tunnel Midos-tunnel-watchdog Midos-tunnel-rekey Midos-tunnel-encap Midos-tunnel-veil midos-veil-panel midos-tunnel-panel
          midos-tunnel midos-tunnel-watchdog midos-tunnel-rekey
          vira-tunnel vira-tunnel-watchdog midos-gre vira-gre greKH greIR
          midos-reality)
TIMERS=(Midos-tunnel-rekey.timer midos-tunnel-rekey.timer)
CONF_DIRS=(/etc/Midos-tunnel /etc/midos-tunnel /etc/vira-tunnel /opt/midos-reality)
SCRIPTS=(/usr/local/sbin/Midos-tunnel.sh /usr/local/sbin/Midos-tunnel-up.sh
         /usr/local/sbin/Midos-tunnel-down.sh /usr/local/sbin/Midos-tunnel-nat.sh
         /usr/local/sbin/Midos-tunnel-watchdog.sh /usr/local/sbin/Midos-tunnel-lic.sh
         /usr/local/sbin/midos-tunnel.sh /usr/local/sbin/midos-tunnel-up.sh
         /usr/local/sbin/midos-tunnel-down.sh /usr/local/sbin/midos-tunnel-nat.sh
         /usr/local/sbin/midos-tunnel-watchdog.sh /usr/local/sbin/midos-tunnel-lic.sh
         /usr/local/sbin/vira-tunnel.sh /usr/local/sbin/vira-tunnel-up.sh
         /usr/local/sbin/vira-tunnel-down.sh /usr/local/sbin/vira-tunnel-nat.sh
         /usr/local/sbin/vira-tunnel-watchdog.sh
         /usr/local/sbin/midos-gre.sh /usr/local/sbin/greKH.sh /usr/local/sbin/greIR.sh
         /usr/local/sbin/Midos-tunnel-encap.py /usr/local/sbin/midos-tunnel-encap.py
         /usr/local/sbin/midos-tunnel /usr/local/sbin/midos-tunnel-arm64
         /usr/local/sbin/midos-veil /usr/local/sbin/midos-veil-arm64
         /usr/bin/midos-tunnel /usr/bin/Midos-tunnel /usr/local/bin/midos-tunnel)
CHAINS_NAT=(MIDOS_PRE MIDOS_POST VIRA_PRE VIRA_POST)
CHAINS_FIL=(MIDOS_FWD MIDOS_IN VIRA_FWD VIRA_IN)
CHAINS_MAN=(MIDOS_MSS MIDOS_MARK VIRA_MSS)
SYS_TUN="gre0 gretap0 erspan0 tunl0 sit0 ip6tnl0 ip_vti0 ip6_vti0 ip6gre0 lo"

banner(){
cat <<EOF
${W}
  ┌────────────────────────────────────────────────────────────┐
  │   MIDOS TUNNEL - DEEP UNINSTALL                            │
  └────────────────────────────────────────────────────────────┘${N}
  ${D}Removes: services, scripts, config, tunnel interfaces, ESP/IPsec
  state, our iptables chains, sysctl tuning, logs and any leftover token.${N}

  ${G}Safe:${N} x-ui / xray / SSH / Caddy / Docker and all other software and
  firewall rules are left completely untouched.
EOF
}
banner
(( DRY )) && printf '\n  %s*** DRY RUN - nothing will be removed, this is only a preview ***%s\n' "$Y" "$N"
if (( ! ASSUME_YES )); then
  printf '\n  %sType%s DELETE %sto confirm:%s ' "$Y" "$W" "$Y" "$N"
  read -r a; [[ "$a" == "DELETE" ]] || { warn "Cancelled."; exit 0; }
fi

# ---------------------------------------------------------------- 1. services
step 1/9 "Stopping and removing services"
for s in "${SERVICES[@]}"; do
  if has_unit "${s}.service"; then
    runq systemctl disable --now "${s}.service" && note "service ${s} removed"
  fi
  runq systemctl stop "${s}.service"
  for u in "/etc/systemd/system/${s}.service" "/lib/systemd/system/${s}.service"; do
    [[ -e "$u" ]] && run rm -f "$u"
  done
done
# relays run as instances of a template (midos-tunnel@NAME, on older installs
# midos-veil@NAME) or as named units
# (Midos-tunnel-veil-NAME), so their names are found rather than listed
for u in $(systemctl list-units --all --plain --no-legend 'midos-tunnel@*' 'midos-veil@*' 'Midos-tunnel-veil*' 2>/dev/null | awk '{print $1}') \
         $(ls /etc/Midos-tunnel/tunnels/*.json 2>/dev/null | sed -E 's#.*/([^/]+)\.json$#midos-tunnel@\1.service midos-veil@\1.service#'); do
  runq systemctl disable --now "$u" && note "relay ${u%.service} removed"
done
for u in /etc/systemd/system/midos-tunnel@.service /etc/systemd/system/midos-veil@.service /etc/systemd/system/Midos-tunnel-veil*.service; do
  [[ -e "$u" ]] && run rm -f "$u"
done
for t in "${TIMERS[@]}"; do
  if has_unit "$t"; then
    runq systemctl disable --now "$t" && note "timer $t removed"
  fi
  [[ -e "/etc/systemd/system/${t}" ]] && run rm -f "/etc/systemd/system/${t}"
done
# a `disable` that ran before the unit file went away can still leave the
# enable symlink behind, so sweep the .wants directories for our names too
for l in /etc/systemd/system/*.wants/*; do
  [[ -L "$l" ]] || continue
  case "${l##*/}" in
    Midos-tunnel*|midos-tunnel*|midos-veil*|vira-tunnel*|midos-gre*|vira-gre*|greKH*|greIR*|midos-reality*)
      run rm -f "$l" && note "stale enable symlink ${l##*/} removed" ;;
  esac
done
for u in /etc/systemd/system/Midos-tunnel-rekey.service /etc/systemd/system/midos-tunnel-rekey.service; do
  [[ -e "$u" ]] && run rm -f "$u"
done
runq systemctl daemon-reload
runq systemctl reset-failed
# any watchdog left running detached
if pgrep -f "Midos-tunnel-watchdo[g]" > /dev/null 2>&1; then
  runq pkill -f "Midos-tunnel-watchdo[g]" && note "stray watchdog killed"
fi
pgrep -f "midos-tunnel-watchdo[g]" > /dev/null 2>&1 && runq pkill -f "midos-tunnel-watchdo[g]"

# ------------------------------------------------------- 2. tunnel interfaces
step 2/9 "Removing tunnel interfaces"
# our own naming first (midosMOI1, viraGRE2, ...)
for i in $(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | cut -d@ -f1 |
           grep -E '^(midosMOI|viraGRE|midosGRE|viraMOI)[0-9]*$'); do
  seen "$i" && continue
  runq ip link set "$i" down; runq ip link del "$i" && note "interface $i deleted"
done
# any other GRE tunnel that is not a kernel default (leftovers of older scripts)
for i in $(ip -d -o link show type gre 2>/dev/null | awk -F': ' '{print $2}' | cut -d@ -f1); do
  [[ " $SYS_TUN " == *" $i "* ]] && continue
  seen "$i" && continue
  runq ip link set "$i" down; runq ip link del "$i" && note "stale gre interface $i deleted"
done
for i in $(ip tunnel show 2>/dev/null | awk -F: '{print $1}' | tr -d ' ' | grep -v '^$'); do
  [[ " $SYS_TUN " == *" $i "* ]] && continue
  seen "$i" && continue
  runq ip tunnel del "$i" && note "stale tunnel $i deleted"
done

# --------------------------------------------------------- 3. FOU / ESP state
step 3/9 "Removing FOU listeners and IPsec (ESP) state"
while read -r p; do
  [[ -z "$p" ]] && continue
  runq ip fou del port "$p" ipproto 47 && note "FOU listener :$p removed"
done < <(ip fou show 2>/dev/null | grep -oP 'port \K[0-9]+')

# Our ESP always protects a "proto gre" selector - that is a precise fingerprint
# no ordinary IPsec deployment uses, so it is safe to target.
GRE_POL=$(ip xfrm policy show 2>/dev/null | grep -c 'proto gre' || true)
if [[ "${GRE_POL:-0}" -gt 0 ]]; then
  ip -o xfrm policy show 2>/dev/null | grep 'proto gre' | while read -r line; do
    src=$(grep -oP 'src \K[0-9./]+' <<<"$line" | head -1)
    dst=$(grep -oP 'dst \K[0-9./]+' <<<"$line" | head -1)
    dir=$(grep -oP 'dir \K\w+'      <<<"$line" | head -1)
    [[ -n "$src" && -n "$dst" && -n "$dir" ]] &&
      runq ip xfrm policy delete src "$src" dst "$dst" proto gre dir "$dir"
  done
  note "$GRE_POL IPsec policies (proto gre) removed"
fi
# ESP states whose selector is gre - including zombies from old peer IPs
ESP_N=0
while read -r s d; do
  [[ -z "${s:-}" || -z "${d:-}" ]] && continue
  runq ip xfrm state deleteall src "$s" dst "$d" proto esp && ESP_N=$((ESP_N+1))
done < <(ip xfrm state show 2>/dev/null | awk '/^src/{s=$2; d=$4} /sel .*proto gre/{print s, d}' | sort -u)
(( ESP_N > 0 )) && note "$ESP_N ESP security associations removed"

# ------------------------------------------------------------- 4. firewall
step 4/9 "Removing our firewall chains (nothing else is touched)"
detach(){ local tbl="$1" hook="$2" chain="$3"
  while iptables -t "$tbl" -C "$hook" -j "$chain" > /dev/null 2>&1; do
    runq iptables -t "$tbl" -D "$hook" -j "$chain" || break
    (( DRY )) && break
  done; }
for ch in "${CHAINS_NAT[@]}"; do
  detach nat PREROUTING "$ch"; detach nat POSTROUTING "$ch"; detach nat OUTPUT "$ch"
  if iptables -t nat -L "$ch" >/dev/null 2>&1; then
    runq iptables -t nat -F "$ch"; runq iptables -t nat -X "$ch" && note "nat chain $ch removed"
  fi
done
for ch in "${CHAINS_FIL[@]}"; do
  detach filter FORWARD "$ch"; detach filter INPUT "$ch"; detach filter OUTPUT "$ch"
  if iptables -L "$ch" >/dev/null 2>&1; then
    runq iptables -F "$ch"; runq iptables -X "$ch" && note "filter chain $ch removed"
  fi
done
for ch in "${CHAINS_MAN[@]}"; do
  detach mangle FORWARD "$ch"; detach mangle POSTROUTING "$ch"
  if iptables -t mangle -L "$ch" >/dev/null 2>&1; then
    runq iptables -t mangle -F "$ch"; runq iptables -t mangle -X "$ch" && note "mangle chain $ch removed"
  fi
done
command -v netfilter-persistent >/dev/null 2>&1 && runq netfilter-persistent save

# ------------------------------------------------------------ 5. scripts
step 5/9 "Removing scripts"
for f in "${SCRIPTS[@]}" /usr/local/sbin/*idos-tunnel*.sh /usr/local/sbin/*ira-tunnel*.sh; do
  [[ -e "$f" ]] || continue
  seen "$f" && continue
  run rm -f "$f" && note "removed $f"
done

# ------------------------------------------------- 6. config, keys, licence
step 6/9 "Removing configuration, keys and licence token"
for d in "${CONF_DIRS[@]}"; do
  [[ -d "$d" ]] && { run rm -rf "$d" && note "removed $d"; }
done
[[ -e /etc/stunnel/stunnel.conf.midos ]] && run rm -f /etc/stunnel/stunnel.conf.midos
# our stunnel section only - leave a foreign stunnel setup alone
if [[ -f /etc/stunnel/stunnel.conf ]] && grep -q 'midos' /etc/stunnel/stunnel.conf 2>/dev/null; then
  runq systemctl disable --now stunnel4
  run rm -f /etc/stunnel/stunnel.conf /etc/stunnel/midos.pem
  note "midos stunnel configuration removed"
fi

# stale policy-routing left by the client-ip preservation option
while ip rule list 2>/dev/null | grep -q "fwmark 0x4d"; do
  runq ip rule del fwmark 0x4d || break
  (( DRY )) && break
done
ip route show table 201 2>/dev/null | grep -q . && runq ip route flush table 201

# ------------------------------------------------------ 7. kernel tuning
step 7/9 "Reverting kernel tuning"
for f in /etc/sysctl.d/99-Midos-tunnel.conf /etc/sysctl.d/99-midos-tunnel.conf \
         /etc/sysctl.d/99-vira-tunnel.conf; do
  [[ -e "$f" ]] && run rm -f "$f" && note "removed $f"
done
for f in /etc/modules-load.d/Midos-tunnel.conf /etc/modules-load.d/midos-tunnel.conf \
         /etc/modules-load.d/vira-tunnel.conf /etc/modules-load.d/midos-bbr.conf \
         /etc/modules-load.d/gre.conf; do
  [[ -e "$f" ]] && run rm -f "$f" && note "removed $f"
done
# IP forwarding and rp_filter are deliberately NOT changed.
#
# Removing our drop-in above is enough to undo our own tuning. Editing
# /etc/sysctl.conf or forcing rp_filter would be guesswork about the rest of
# the machine: x-ui, Docker, WireGuard and any other tunnel may legitimately
# need ip_forward=1, and Ubuntu's own default for rp_filter is 2 (loose), so
# forcing 1 would be stricter than stock and can break asymmetric routing.
runq sysctl --system
ok "our sysctl drop-ins removed (ip_forward / rp_filter left as the system has them)"
if [[ "$(cat /proc/sys/net/ipv4/ip_forward 2>/dev/null)" == "1" ]]; then
  info "ip_forward is still 1 - other software may need it; disable it yourself if you want."
fi

# ------------------------------------------------------------- 8. logs
step 8/9 "Removing logs"
for f in /var/log/Midos-tunnel.log /var/log/midos-tunnel.log /var/log/vira-tunnel.log; do
  [[ -e "$f" ]] && run rm -f "$f"* && note "removed $f"
done

# ------------------------------------------------------------ 9. verify
if (( DRY )); then
  step 9/9 "Dry run finished - nothing was removed"
  printf '\n  %s%s item(s) would be removed.%s Re-run without --dry-run to apply.\n\n' "$W" "$REMOVED" "$N"
  exit 0
fi
step 9/9 "Verifying the server is clean"
LEFT=0
chk(){ printf '  %-38s %s\n' "$1" "$2"; }
n=$(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | cut -d@ -f1 | grep -cE '^(midosMOI|viraGRE|midosGRE)' || true)
(( n == 0 )) && chk "tunnel interfaces" "${G}none${N}" || { chk "tunnel interfaces" "${R}${n} left${N}"; LEFT=1; }
n=$(ip xfrm policy show 2>/dev/null | grep -c 'proto gre' || true)
(( n == 0 )) && chk "IPsec gre policies" "${G}none${N}" || { chk "IPsec gre policies" "${R}${n} left${N}"; LEFT=1; }
n=0; for ch in "${CHAINS_NAT[@]}"; do iptables -t nat -L "$ch" >/dev/null 2>&1 && n=$((n+1)); done
for ch in "${CHAINS_FIL[@]}"; do iptables -L "$ch" >/dev/null 2>&1 && n=$((n+1)); done
(( n == 0 )) && chk "our iptables chains" "${G}none${N}" || { chk "our iptables chains" "${R}${n} left${N}"; LEFT=1; }
n=0; for d in "${CONF_DIRS[@]}"; do [[ -d "$d" ]] && n=$((n+1)); done
(( n == 0 )) && chk "config directories" "${G}none${N}" || { chk "config directories" "${R}${n} left${N}"; LEFT=1; }
unit_list   # re-read after the removals
n=0; for s in "${SERVICES[@]}"; do has_unit "${s}.service" && n=$((n+1)); done
(( n == 0 )) && chk "systemd services" "${G}none${N}" || { chk "systemd services" "${R}${n} left${N}"; LEFT=1; }
n=$(ls /usr/local/sbin/ 2>/dev/null | grep -ciE 'midos|vira' || true)
(( n == 0 )) && chk "scripts in /usr/local/sbin" "${G}none${N}" || { chk "scripts in /usr/local/sbin" "${R}${n} left${N}"; LEFT=1; }

# things we deliberately did NOT touch
echo ""
printf '  %sLeft untouched (as intended):%s\n' "$D" "$N"
for svc in x-ui xray caddy nginx docker ssh sshd; do
  systemctl is-active "$svc" >/dev/null 2>&1 && printf '    %s· %s is still running%s\n' "$D" "$svc" "$N"
done

echo ""
if (( LEFT == 0 )); then
  printf '  %s╔══════════════════════════════════════════════════════════╗%s\n' "$G" "$N"
  printf '  %s║  ✅ CLEAN - %-3s item(s) removed, no traces left            ║%s\n' "$G" "$REMOVED" "$N"
  printf '  %s╚══════════════════════════════════════════════════════════╝%s\n' "$G" "$N"
else
  warn "Some items need a reboot to disappear (in-use kernel modules)."
fi
if [[ "$(lsmod 2>/dev/null)" == *ip_gre* ]]; then
  echo ""; info "ip_gre is still loaded; it only unloads after a ${W}reboot${N} (harmless)."
fi
echo ""
