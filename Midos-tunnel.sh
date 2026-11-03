#!/bin/bash
# FREE EDITION - no licence required (built by tools/make-free-edition.py)
# ============================================================
#  Midos Tunnel v3.4.0 - Professional Multi-Server MOI Tunnel Manager
#  SSH-Safe | DPI-Resistant | Auto-Tuned | Multi-Peer | Nuclear Uninstall
# ------------------------------------------------------------
#  Topology:
#    One IRAN server  <----N tunnels---->  N KHAREJ servers
#    Each KHAREJ peer gets its own MOI interface, its own /30,
#    its own MOI key / FOU port / ESP SPI pair.
#
#  Distribution modes on IRAN (menu 6):
#    failover : all traffic to the highest-priority HEALTHY peer,
#               the rest are hot spares (watchdog switches in seconds)
#    balance  : new connections spread over all healthy peers by weight
#    ports    : every peer owns its own TCP/UDP port list
#
#  Transport modes:
#    esp    : MOI + IPsec ESP AES-256-GCM (static keys)  [DEFAULT]
#    gre    : plain MOI (IP proto 47)            - fastest, most detectable
#    fou    : MOI-over-UDP (RFC 7510 / FOU)      - hides proto 47
#    ipsec  : MOI-over-IPsec via strongswan/IKEv2
# ============================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
WHITE='\033[1;37m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'
G1='\033[38;5;220m'
G2='\033[38;5;221m'
G3='\033[38;5;178m'
G4='\033[38;5;172m'
G5='\033[38;5;136m'
G6='\033[38;5;214m'
G7='\033[38;5;208m'
G8='\033[38;5;179m'
BG_GOLD='\033[48;5;136m'

# The installer reads this exact line to decide whether the copy on a server is
# stale. Without it, install.sh could only ask "is there a script?" and happily
# left every existing server frozen on an old release. Bump it on each release.
TUNNEL_VERSION="3.4.0"

CONFIG_DIR="/etc/Midos-tunnel"
CONFIG_FILE="${CONFIG_DIR}/config"
HEALTH_FILE="${CONFIG_DIR}/health"
UP_SCRIPT="/usr/local/sbin/Midos-tunnel-up.sh"
DOWN_SCRIPT="/usr/local/sbin/Midos-tunnel-down.sh"
NAT_SCRIPT="/usr/local/sbin/Midos-tunnel-nat.sh"
WD_SCRIPT="/usr/local/sbin/Midos-tunnel-watchdog.sh"
LIC_SCRIPT="/usr/local/sbin/Midos-tunnel-lic.sh"
ENCAP_SCRIPT="/usr/local/sbin/Midos-tunnel-encap.py"
# the tunnel engine + panel binary (older installs kept it under another name;
# "midos-tunnel migrate" moves it here)
VEIL_BIN="/usr/local/sbin/midos-tunnel"
[[ -x "$VEIL_BIN" || ! -x /usr/local/sbin/midos-veil ]] || VEIL_BIN="/usr/local/sbin/midos-veil"
VEIL_CONF="${CONFIG_DIR}/veil.json"
SERVICE_FILE="/etc/systemd/system/Midos-tunnel.service"
REKEY_SERVICE_FILE="/etc/systemd/system/Midos-tunnel-rekey.service"
REKEY_TIMER_FILE="/etc/systemd/system/Midos-tunnel-rekey.timer"
WD_SERVICE_FILE="/etc/systemd/system/Midos-tunnel-watchdog.service"
ENCAP_SERVICE_FILE="/etc/systemd/system/Midos-tunnel-encap.service"
VEIL_SERVICE_FILE="/etc/systemd/system/Midos-tunnel-veil.service"
SYSCTL_FILE="/etc/sysctl.d/99-Midos-tunnel.conf"
LOG_FILE="/var/log/Midos-tunnel.log"
# every peer interface is TUNNEL_BASE + its index: midosMOI1, midosMOI2, ...
# iptables matches them all at once with "midosMOI+"
TUNNEL_BASE="midosMOI"
TUNNEL_NAME="midosMOI"          # legacy single-tunnel name (uninstall/migration)
MIDOS_PRE="MIDOS_PRE"
MIDOS_POST="MIDOS_POST"
MIDOS_FWD="MIDOS_FWD"
MIDOS_IN="MIDOS_IN"
MIDOS_MARK="MIDOS_MARK"

# ---- defaults (overridable from menu 5 / 6) ----
DEF_PRIV_BASE="11.10.10"        # peer k uses .(4k-3) [IRAN] and .(4k-2) [KHAREJ]
DEF_TUNNEL_MTU="1460"
DEF_BALANCE_MODE="ports"        # ports | balance | failover  (ports = every server works independently)
DEF_TCP_PORTS="443"
DEF_UDP_PORTS="443"
DEF_MODE="esp"
DEF_WATCHDOG="1"
DEF_WD_INTERVAL="20"
DEF_ESP_ROTATE_SEC="3600"       # esp mode: new keys + new SPIs every hour (0 = static)
DEF_ESP_TFC_PAD="0"             # esp mode: pad packets up to N bytes (0 = off, costs bandwidth)
# TCP MSS advertised inside the tunnel. 0 derives it from the MTU, which is
# right whenever the MTU itself is right. It is kept separate because the two
# are not always the same problem: a path can carry our MTU and still sit
# behind a middlebox that mangles large segments, and lowering MSS alone fixes
# that without shrinking every UDP packet as well.
DEF_TCP_MSS="0"

# Named tuning profiles. A profile only sets defaults; anything set explicitly
# afterwards still wins. They exist because the individual knobs below are easy
# to get wrong in a way that is worse than leaving them alone - a shaper set to
# the wrong rate hurts more than no shaper, and an over-large MSS black-holes
# traffic while still passing a ping.
#   balanced    - safe everywhere; no shaping, MSS from MTU
#   low_latency - small queues, favour responsiveness over peak throughput
#   throughput  - deeper queues and bigger buffers for bulk transfer
DEF_TUNING_PROFILE="balanced"

# Preserve the real client address end to end.
# Off by default the IRAN side rewrites every relayed packet to its own end of
# the /30, so the service abroad logs the relay instead of the user and any
# per-IP limit there sees one client. With this on, the relayed packet keeps
# its original source and the KHAREJ side sends the reply back through the
# tunnel using a policy route, because its own default route would otherwise
# send it straight out of its WAN where it would be dropped as a martian.
# It needs matching support on BOTH ends, so both must have it enabled.
DEF_KEEP_CLIENT_IP="0"
MAX_PEERS=32
# Egress shaping on the tunnel, in Mbit. Set a little BELOW the real bottleneck
# so the queue forms in our own AQM instead of in the ISP's dumb buffer - that
# is what keeps latency flat while someone else is pulling a big download.
# 0 disables shaping and falls back to plain fq_codel.
#
# The default is 0 on purpose. A shaper only helps when the number matches the
# link's real bottleneck; guessing it wrong is worse than not shaping at all.
# cake at a high rate also costs real CPU, and on a busy relay that showed up as
# sustained high load with no benefit. fq_codel alone already controls
# bufferbloat well. Set a rate in the menu once the true bottleneck is known.
DEF_SHAPE_MBIT="0"

ROLE=""
IRAN_IP=""
KHAREJ_IP=""            # on a KHAREJ server: its own public IP
SELF_IP=""
PEER_INDEX="1"          # on a KHAREJ server: which slot it occupies on IRAN
PEER_COUNT=0
PRIV_BASE="$DEF_PRIV_BASE"
TUNNEL_MODE="$DEF_MODE"
TUNNEL_MTU="$DEF_TUNNEL_MTU"
TCP_MSS="${TCP_MSS:-$DEF_TCP_MSS}"
TUNING_PROFILE="${TUNING_PROFILE:-$DEF_TUNING_PROFILE}"
KEEP_CLIENT_IP="${KEEP_CLIENT_IP:-$DEF_KEEP_CLIENT_IP}"
BALANCE_MODE="$DEF_BALANCE_MODE"
EXCLUDE_TCP_PORTS=""
EXCLUDE_UDP_PORTS=""
IPSEC_PSK=""
IKE_PORT=""
WATCHDOG="$DEF_WATCHDOG"
WD_INTERVAL="$DEF_WD_INTERVAL"
SHAPE_MBIT="$DEF_SHAPE_MBIT"
SSH_PORT="22"

# legacy / system tunnels that must never be deleted
SYS_TUNNELS="tunl0 sit0 ip6tnl0 ip6gre0 gre0 ip6_vti0 erspan0"

# ──────────────────── DOWNLOADS ────────────────────
# Free edition: no licence check. The backup-path helper and the deep
# uninstaller are still downloaded from here.
# release assets (midos-tunnel, midos-tunnel-arm64, Midos-tunnel.sh, uninstall.sh)
DOWNLOAD_BASE="https://github.com/1Rootzen/midos-tunnel/releases/latest/download"

# ──────────────────── LOGO ────────────────────

show_logo() {
    clear
    echo ""
    echo -e "${G6}    ╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${G6}    ║${NC}                                                              ${G6}║${NC}"
    echo -e "${G6}    ║${G2}   ███╗   ███╗ ██╗ ██████╗  ██████╗  ███████╗                  ${G6}║${NC}"
    echo -e "${G6}    ║${G3}   ████╗ ████║ ██║ ██╔══██╗██╔═══██╗ ██╔════╝                  ${G6}║${NC}"
    echo -e "${G6}    ║${G4}   ██╔████╔██║ ██║ ██║  ██║██║   ██║ ███████╗                  ${G6}║${NC}"
    echo -e "${G6}    ║${G6}   ██║╚██╔╝██║ ██║ ██║  ██║██║   ██║ ╚════██║                  ${G6}║${NC}"
    echo -e "${G6}    ║${G7}   ██║ ╚═╝ ██║ ██║ ██████╔╝╚██████╔╝ ███████║   ${WHITE}${BOLD}T U N N${NC}       ${G6}║${NC}"
    echo -e "${G6}    ║${G8}   ╚═╝     ╚═╝ ╚═╝ ╚═════╝  ╚═════╝  ╚══════╝                  ${G6}║${NC}"
    echo -e "${G6}    ║${NC}                                                              ${G6}║${NC}"
    echo -e "${G6}    ╠══════════════════════════════════════════════════════════════╣${NC}"
    echo -e "${G6}    ║${NC}  ${G2}★${NC} ${WHITE}Midos Tunnel — Multi-Server MOI Tunnel${NC}   ${DIM}Version 3.2${NC}    ${G6}  ║${NC}"
    echo -e "${G6}    ║${NC}  ${G3}★${NC} ${DIM}Failover • DPI-Resistant • Watchdog • Free Edition${NC}      ${G6}  ║${NC}"
    echo -e "${G6}    ╚══════════════════════════════════════════════════════════════╝${NC}"
    echo ""
}

# ──────────────────── HELPERS ────────────────────

ok()   { echo -e "    ${GREEN}  ✔  ${NC}$1"; }
err()  { echo -e "    ${RED}  ✘  ${NC}$1"; }
warn() { echo -e "    ${YELLOW}  ⚠  ${NC}$1"; }
info() { echo -e "    ${CYAN}  ℹ  ${NC}$1"; }
stp()  { echo -e "    ${G2}[Step $1]${NC} ${WHITE}$2${NC}"; }

check_root() {
    [[ $EUID -ne 0 ]] && { err "Run as root: ${WHITE}sudo bash $0${NC}"; exit 1; }
}

validate_ip() {
    local ip="$1"
    [[ $ip =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]] || return 1
    IFS='.' read -ra O <<< "$ip"
    for o in "${O[@]}"; do ((10#$o > 255)) && return 1; done
    return 0
}

# port list like "443,8443,2000-3000" (multiport: max 15 entries)
validate_ports() {
    local list="$1"
    [[ -z "$list" ]] && return 0
    # a term is  443  |  2000-3000  |  12053:2053  (external:peer-side)
    local term='[0-9]+(-[0-9]+)?(:[0-9]+)?'
    [[ $list =~ ^${term}(,${term})*$ ]] || return 1
    return 0
}

detect_local_ip() {
    ip -4 route get 8.8.8.8 2>/dev/null | grep -oP 'src \K[0-9.]+' | head -1
}

detect_default_iface() {
    ip -4 route get 8.8.8.8 2>/dev/null | grep -oP 'dev \K\S+' | head -1
}

detect_ssh_port() {
    local p
    p=$(grep -oP '^\s*Port\s+\K[0-9]+' /etc/ssh/sshd_config 2>/dev/null | head -1)
    [[ -z "$p" ]] && p=$(ss -tlnp 2>/dev/null | grep -i sshd | grep -oP ':\K[0-9]+' | head -1)
    echo "${p:-22}"
}

# deterministic pseudo-random value derived from the IP pair,
# so both servers compute the SAME value with no manual sync
derive_num() {
    local seed="$1" lo="$2" hi="$3" n
    n=$(printf '%s' "$seed" | cksum | cut -d' ' -f1)
    echo $(( lo + n % (hi - lo + 1) ))
}

is_sys_tunnel() {
    local name="$1" st
    for st in $SYS_TUNNELS; do
        [[ "$name" == "$st" ]] && return 0
    done
    return 1
}

has_module() { modinfo "$1" > /dev/null 2>&1; }

ensure_modules() {
    modprobe gre 2>/dev/null || true
    modprobe ip_gre 2>/dev/null || true
    local mods="gre\nip_gre\n"
    if [[ "$TUNNEL_MODE" == "fou" ]]; then
        modprobe fou 2>/dev/null || true
        modprobe ip_gre 2>/dev/null || true
        mods="${mods}fou\n"
    fi
    if [[ "$TUNNEL_MODE" == "ipsec" || "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" ]]; then
        modprobe esp4 2>/dev/null || true
        modprobe xfrm4_tunnel 2>/dev/null || true
        modprobe xfrm_user 2>/dev/null || true
        modprobe authenc 2>/dev/null || true
        mods="${mods}esp4\nxfrm_user\n"
    fi
    printf "%b" "$mods" > /etc/modules-load.d/Midos-tunnel.conf
}

# ──────────────────── PEER MODEL ────────────────────
# A "peer" is one remote server. On IRAN there are 1..N of them (the KHAREJ
# servers); on a KHAREJ server there is exactly one (the IRAN server).
# Everything about a peer lives in shell variables PEER_<n>_<FIELD> so the
# whole table can be flat-saved into the config file and sourced by the
# generated up/down/nat/watchdog scripts without any parsing code.
#
#   IP      remote public IP          IDX     slot number (decides the /30)
#   NAME    label shown in the UI     IFACE   midosMOI<IDX>
#   LOCAL   our tunnel IP             REMOTE  peer tunnel IP
#   NET     the /30 of this pair      KEY     MOI key      FOU  FOU udp port
#   WEIGHT  share in balance mode     TCP/UDP port lists (ports mode)
#   ENABLED 1/0 - a disabled peer keeps its config but carries no traffic

pget() { local v="PEER_${1}_${2}"; echo "${!v}"; }
pset() { printf -v "PEER_${1}_${2}" '%s' "$3"; }

# /30 layout: slot k owns PRIV_BASE.(4k-4)/30 -> .(4k-3) IRAN / .(4k-2) KHAREJ
slot_iran_ip()   { echo "${PRIV_BASE}.$(( 4 * $1 - 3 ))"; }
slot_kharej_ip() { echo "${PRIV_BASE}.$(( 4 * $1 - 2 ))"; }
slot_net()       { echo "${PRIV_BASE}.$(( 4 * $1 - 4 ))/30"; }

# derived, identical on both ends because the seed is the ordered IP pair
peer_gre_key()  { derive_num "midos-key|${1}|${2}" 1 2000000000; }
peer_fou_port() { derive_num "midos-fou|${1}|${2}" 20000 60000; }
# ESP-in-UDP carries the whole tunnel inside ordinary UDP. Port 4500 is the
# standard NAT-T port and therefore a signature of its own, so each peer gets
# its own high port derived from the IP pair, exactly like the FOU one.
peer_udp_port() { derive_num "midos-espudp|${1}|${2}" 20000 60000; }

peer_index_used() {
    local idx="$1" i
    for ((i = 1; i <= PEER_COUNT; i++)); do
        [[ "$(pget "$i" IDX)" == "$idx" ]] && return 0
    done
    return 1
}

next_free_index() {
    local idx=1
    while peer_index_used "$idx"; do idx=$((idx + 1)); done
    echo "$idx"
}

peer_ip_used() {
    local ip="$1" i
    for ((i = 1; i <= PEER_COUNT; i++)); do
        [[ "$(pget "$i" IP)" == "$ip" ]] && return 0
    done
    return 1
}

# ports mode only: a TCP/UDP port may belong to exactly ONE peer, because a
# DNAT rule sends a port to a single destination. If two peers claim the same
# port, iptables uses the first rule and the second peer silently gets nothing.
# Prints one line per clashing port: "<proto> <port>: <nameA> + <nameB>".
ports_collision() {
    local proto i spec item lo hi p n=0
    for proto in TCP UDP; do
        local -A seen=()
        for ((i = 1; i <= PEER_COUNT; i++)); do
            [[ "$(pget "$i" ENABLED)" == "1" ]] || continue
            spec="$(pget "$i" "$proto")"
            [[ -z "$spec" ]] && continue
            local IFS=','; local items=($spec); unset IFS
            for item in "${items[@]}"; do
                # "12053:2053" clashes on the EXTERNAL port (12053); the
                # internal one may legitimately repeat across peers.
                item="${item%%:*}"
                if [[ "$item" == *-* ]]; then lo="${item%-*}"; hi="${item#*-}"; else lo="$item"; hi="$item"; fi
                # guard against a pathological huge range locking bash up
                (( hi - lo > 8192 )) && hi=$(( lo + 8192 ))
                for ((p = lo; p <= hi; p++)); do
                    if [[ -n "${seen[$p]:-}" ]]; then
                        echo "${proto} ${p}: $(pget "${seen[$p]}" NAME) + $(pget "$i" NAME)"
                        n=$((n + 1)); (( n >= 20 )) && { echo "... (more)"; return 1; }
                    else
                        seen[$p]="$i"
                    fi
                done
            done
        done
        unset seen
    done
    (( n > 0 )) && return 1 || return 0
}

# add_peer <public-ip> <label> <slot> [weight] [tcp-ports] [udp-ports]
# On IRAN the pair seed is (IRAN_IP, peer-ip); on KHAREJ it is (IRAN_IP, own-ip),
# so both servers derive the very same MOI key and FOU port with no manual sync.
add_peer() {
    local ip="$1" name="$2" idx="$3" weight="${4:-1}" tcp="${5:-}" udp="${6:-}"
    local n=$((PEER_COUNT + 1)) a b
    if [[ "$ROLE" == "IRAN" ]]; then a="$IRAN_IP"; b="$ip"; else a="$IRAN_IP"; b="$SELF_IP"; fi
    pset "$n" IP      "$ip"
    pset "$n" NAME    "${name:-peer${idx}}"
    pset "$n" IDX     "$idx"
    pset "$n" IFACE   "${TUNNEL_BASE}${idx}"
    if [[ "$ROLE" == "IRAN" ]]; then
        pset "$n" LOCAL  "$(slot_iran_ip   "$idx")"
        pset "$n" REMOTE "$(slot_kharej_ip "$idx")"
    else
        pset "$n" LOCAL  "$(slot_kharej_ip "$idx")"
        pset "$n" REMOTE "$(slot_iran_ip   "$idx")"
    fi
    pset "$n" NET     "$(slot_net "$idx")"
    pset "$n" KEY     "$(peer_gre_key  "$a" "$b")"
    pset "$n" FOU     "$(peer_fou_port "$a" "$b")"
    pset "$n" UDPPORT "$(peer_udp_port "$a" "$b")"
    pset "$n" WEIGHT  "$weight"
    pset "$n" TCP     "$tcp"
    pset "$n" UDP     "$udp"
    pset "$n" ENABLED "1"
    PEER_COUNT=$n
}

clear_peers() {
    local i f
    for ((i = 1; i <= PEER_COUNT; i++)); do
        for f in IP NAME IDX IFACE LOCAL REMOTE NET KEY FOU UDPPORT WEIGHT TCP UDP ENABLED; do
            unset "PEER_${i}_${f}"
        done
    done
    PEER_COUNT=0
}

# drop peer <n> and close the gap so the table stays 1..PEER_COUNT
remove_peer() {
    local del="$1" i f src
    for ((i = del; i < PEER_COUNT; i++)); do
        src=$((i + 1))
        for f in IP NAME IDX IFACE LOCAL REMOTE NET KEY FOU UDPPORT WEIGHT TCP UDP ENABLED; do
            pset "$i" "$f" "$(pget "$src" "$f")"
        done
    done
    for f in IP NAME IDX IFACE LOCAL REMOTE NET KEY FOU UDPPORT WEIGHT TCP UDP ENABLED; do
        unset "PEER_${PEER_COUNT}_${f}"
    done
    PEER_COUNT=$((PEER_COUNT - 1))
}

peer_summary_line() {
    local i="$1" en st
    [[ "$(pget "$i" ENABLED)" == "1" ]] && en="${GREEN}on ${NC}" || en="${DIM}off${NC}"
    st="$(peer_health_label "$i")"
    printf "    ${G2}[%d]${NC} %-14s ${CYAN}%-15s${NC} ${DIM}slot${NC} %-3s ${DIM}iface${NC} %-10s %b  %b\n" \
        "$i" "$(pget "$i" NAME)" "$(pget "$i" IP)" "$(pget "$i" IDX)" "$(pget "$i" IFACE)" "$en" "$st"
}

peer_health_label() {
    local i="$1" ifc rem
    ifc="$(pget "$i" IFACE)"; rem="$(pget "$i" REMOTE)"
    if ! ip link show "$ifc" > /dev/null 2>&1; then
        echo "${RED}no-iface${NC}"
    elif ping -c 1 -W 2 -I "$ifc" "$rem" > /dev/null 2>&1; then
        echo "${GREEN}UP${NC}"
    else
        echo "${RED}DOWN${NC}"
    fi
}

# ──────────────────── CONFIG ────────────────────

save_config() {
    local i f
    mkdir -p "$CONFIG_DIR"
    {
        echo "# Midos Tunnel configuration - generated $(date '+%Y-%m-%d %H:%M:%S')"
        echo "CONFIG_VERSION=3"
        echo "ROLE=${ROLE}"
        echo "IRAN_IP=${IRAN_IP}"
        echo "KHAREJ_IP=${KHAREJ_IP}"
        echo "SELF_IP=${SELF_IP}"
        echo "PEER_INDEX=${PEER_INDEX}"
        echo "PRIV_BASE=${PRIV_BASE}"
        echo "TUNNEL_BASE=${TUNNEL_BASE}"
        echo "TUNNEL_MODE=${TUNNEL_MODE}"
        echo "TUNNEL_MTU=${TUNNEL_MTU}"
        echo "TCP_MSS=${TCP_MSS:-0}"
        echo "TUNING_PROFILE=${TUNING_PROFILE:-balanced}"
        echo "KEEP_CLIENT_IP=${KEEP_CLIENT_IP:-0}"
        echo "VEIL_PORT=${VEIL_PORT:-}"
        echo "VEIL_SNI=${VEIL_SNI:-}"
        echo "VEIL_FP=${VEIL_FP:-}"
        echo "VEIL_PEER=${VEIL_PEER:-}"
        echo "BALANCE_MODE=${BALANCE_MODE}"
        echo "EXCLUDE_TCP_PORTS=${EXCLUDE_TCP_PORTS}"
        echo "EXCLUDE_UDP_PORTS=${EXCLUDE_UDP_PORTS}"
        echo "IPSEC_PSK='${IPSEC_PSK}'"
        echo "IKE_PORT=${IKE_PORT}"
        echo "SSH_PORT=${SSH_PORT}"
        echo "WATCHDOG=${WATCHDOG}"
        echo "WD_INTERVAL=${WD_INTERVAL}"
        echo "SHAPE_MBIT=${SHAPE_MBIT}"
        echo "ESP_ROTATE_SEC=${ESP_ROTATE_SEC:-$DEF_ESP_ROTATE_SEC}"
        echo "ESP_TFC_PAD=${ESP_TFC_PAD:-$DEF_ESP_TFC_PAD}"
        echo "PEER_COUNT=${PEER_COUNT}"
        for ((i = 1; i <= PEER_COUNT; i++)); do
            echo "# --- peer ${i}: $(pget "$i" NAME) ---"
            for f in IP NAME IDX IFACE LOCAL REMOTE NET KEY FOU UDPPORT WEIGHT TCP UDP ENABLED; do
                echo "PEER_${i}_${f}='$(pget "$i" "$f")'"
            done
        done
        echo "INSTALL_DATE='${INSTALL_DATE:-$(date '+%Y-%m-%d %H:%M:%S')}'"
    } > "$CONFIG_FILE"
    chmod 600 "$CONFIG_FILE"
}

# v2 installs had exactly one tunnel described by loose variables. Fold that
# into a single peer so an upgrade keeps working without a reinstall.
migrate_legacy_config() {
    [[ "${PEER_COUNT:-0}" -gt 0 ]] && return 0
    [[ -z "${IRAN_IP}" || -z "${KHAREJ_IP}" ]] && return 1
    PRIV_BASE="${PRIV_BASE:-$DEF_PRIV_BASE}"
    TUNNEL_BASE="${TUNNEL_BASE:-midosMOI}"
    PEER_INDEX=1
    PEER_COUNT=0
    if [[ "$ROLE" == "IRAN" ]]; then
        SELF_IP="$IRAN_IP"
        add_peer "$KHAREJ_IP" "kharej1" 1 1 "${FORWARD_TCP_PORTS}" "${FORWARD_UDP_PORTS}"
    else
        SELF_IP="$KHAREJ_IP"
        add_peer "$IRAN_IP" "iran" 1 1 "" ""
    fi
    # v2 called it FORWARD_MODE=all|ports
    if [[ "${FORWARD_MODE:-all}" == "ports" ]]; then
        BALANCE_MODE="ports"
    else
        BALANCE_MODE="failover"
        pset 1 TCP ""; pset 1 UDP ""
    fi
    # the old install used midosMOI with no index; renaming happens on next up
    return 0
}

load_config() {
    [[ -f "$CONFIG_FILE" ]] || return 1
    # shellcheck disable=SC1090
    source "$CONFIG_FILE"
    PEER_COUNT="${PEER_COUNT:-0}"
    if [[ "$PEER_COUNT" -eq 0 ]]; then
        migrate_legacy_config || return 1
        save_config
    fi
    [[ -z "$SELF_IP" ]] && { [[ "$ROLE" == "IRAN" ]] && SELF_IP="$IRAN_IP" || SELF_IP="$KHAREJ_IP"; }
    return 0
}

balance_label() {
    # only the IRAN side distributes traffic
    [[ "${ROLE:-}" == "KHAREJ" ]] && { echo "decided by the IRAN server"; return; }
    case "$1" in
        failover) echo "FAILOVER (one active peer + hot spares)" ;;
        balance)  echo "LOAD BALANCE (weighted, per connection)" ;;
        ports)    echo "PER-PORT (each peer owns its port list)" ;;
        *)        echo "$1" ;;
    esac
}
progress_bar() {
    local w=40 i
    echo -ne "    ${DIM}$2${NC} ["
    for ((i = 0; i <= w; i++)); do
        echo -ne "${G2}█${NC}"
        sleep 0.015
    done
    echo -e "] ${GREEN}Done!${NC}"
}

mode_label() {
    case "$1" in
        gre)   echo "MOI (plain, proto 47)" ;;
        esp)   echo "MOI + IPsec ESP, AES-256-GCM" ;;
        fou)   echo "MOI-over-UDP / FOU" ;;
        espudp) echo "MOI + ESP inside UDP, AES-256-GCM" ;;
        ipsec) echo "MOI-over-IPsec via strongswan" ;;
        *)     echo "$1" ;;
    esac
}

# ──────────────────── IP / MODE INPUT ────────────────────

choose_mode() {
    echo ""
    echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
    echo -e "${G3}    │${NC}  ${G1}🛡${NC}  ${WHITE}${BOLD}TRANSPORT MODE (DPI RESISTANCE)${NC}                          ${G3}│${NC}"
    echo -e "${G3}    ├──────────────────────────────────────────────────────────────┤${NC}"
    echo -e "${G3}    │${NC}  ${G2}[1]${NC} ${WHITE}ESP${NC}   MOI encrypted with IPsec AES-256-GCM ${GREEN}(best)${NC}      ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}Wire shows only opaque ESP - DPI cannot read a byte${NC}     ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}of the inner traffic. Static keys, no daemon needed.${NC}    ${G3}│${NC}"
    echo -e "${G3}    │${NC}  ${G2}[5]${NC} ${WHITE}ESPUDP${NC} Same crypto, wrapped in plain UDP ${GREEN}(stealthier)${NC}  ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}ESP and MOI are bare IP protocols (50 and 47) that a${NC}    ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}single rule can drop network-wide. This looks like${NC}      ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}ordinary UDP instead, and traverses NAT. ~7% slower.${NC}    ${G3}│${NC}"
    echo -e "${G3}    │${NC}  ${G2}[2]${NC} ${WHITE}MOI${NC}   Plain MOI, IP proto 47 - fastest, ${RED}unencrypted${NC}     ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}DPI can read every packet you relay. Speed only.${NC}        ${G3}│${NC}"
    echo -e "${G3}    │${NC}  ${G2}[3]${NC} ${WHITE}FOU${NC}   MOI inside UDP on a random port             ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}Hides proto 47 - but if your ISP polices UDP (most${NC}      ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}Iranian ones do) throughput collapses. Measure first.${NC}   ${G3}│${NC}"
    echo -e "${G3}    │${NC}  ${G2}[4]${NC} ${WHITE}IPSEC${NC} Same as [1] but with strongswan + IKEv2         ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}Rekeying and PFS; needs apt access to install it.${NC}       ${G3}│${NC}"
    echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
    echo ""
    echo -ne "    ${G2}❯${NC} Mode [${CYAN}1${NC}]: "
    read -r m
    case "${m:-1}" in
        2) TUNNEL_MODE="gre" ;;
        3) TUNNEL_MODE="fou" ;;
        4) TUNNEL_MODE="ipsec" ;;
        5) TUNNEL_MODE="espudp" ;;
        *) TUNNEL_MODE="esp" ;;
    esac

    # per-mode MTU: outer path is normally 1500, so subtract only the real overhead
    #   gre   = 20 IP + 8 MOI(with key)              = 28  -> 1460 leaves headroom
    #   esp   = the above + ESP hdr/IV/ICV/pad (~36) = 64  -> 1400 in theory
    #   fou   = 20 IP + 8 UDP + 8 MOI                = 36  -> 1436
    #   ipsec = esp plus IKE/NAT-T framing                 -> 1360
    #
    # esp is set to 1360, not the theoretical 1400, because the outer path is
    # often NOT 1500. Measured on a live four-peer deployment the usable path
    # MTU was 1438 to three peers and only 1394 to a peer behind a carrier NAT.
    # Anything above that black-holes: the TCP handshake completes and every
    # full-size data packet is silently dropped, which looks like "connects but
    # does not work". 1360 keeps headroom on all of them; raise it per install
    # only after measuring with `ping -M do -s`.
    case "$TUNNEL_MODE" in
        gre)    TUNNEL_MTU=1460 ;;
        esp)    TUNNEL_MTU=1360 ;;
        espudp) TUNNEL_MTU=1340 ;;   # esp plus the 8-byte UDP header
        fou)    TUNNEL_MTU=1436 ;;
    esac

    if [[ "$TUNNEL_MODE" == "fou" ]] && ! has_module fou; then
        warn "Kernel module ${WHITE}fou${NC} not available on this kernel."
        warn "Falling back to plain MOI."
        TUNNEL_MODE="gre"
    fi

    if [[ "$TUNNEL_MODE" == "ipsec" || "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" ]]; then
        echo ""
        info "Both servers MUST use the exact same passphrase."
        info "The encryption keys are derived from it, so a mismatch means"
        info "the tunnel comes up but silently drops every packet."
        while [[ -z "$IPSEC_PSK" ]]; do
            echo -ne "    ${G2}❯${NC} Shared passphrase ${DIM}(Enter = generate a strong one)${NC}: "
            read -r IPSEC_PSK
            if [[ -z "$IPSEC_PSK" ]]; then
                IPSEC_PSK=$(openssl rand -base64 48 2>/dev/null | tr -dc 'A-Za-z0-9' | head -c 24)
                [[ ${#IPSEC_PSK} -eq 24 ]] || { IPSEC_PSK=""; err "openssl not found - type a passphrase."; continue; }
                echo ""
                ok "Generated passphrase: ${WHITE}${BOLD}${IPSEC_PSK}${NC}"
                warn "Write it down NOW - every other server needs exactly this one."
                echo ""
            elif [[ ${#IPSEC_PSK} -lt 12 ]]; then
                err "At least 12 characters - every key is derived from it."; IPSEC_PSK=""
            fi
        done
        [[ "$TUNNEL_MODE" == "ipsec" ]] && TUNNEL_MTU=1360
    fi
    ok "Mode: ${CYAN}$(mode_label "$TUNNEL_MODE")${NC}"
}

# ──────────────────── DISTRIBUTION / PORT INPUT ────────────────────

# ports the IRAN server must NEVER hand to a tunnel (SSH first of all)
ask_local_exclusions() {
    local input live_ssh
    SSH_PORT=$(detect_ssh_port)
    live_ssh=$(echo "${SSH_CONNECTION:-}" | awk '{print $4}')
    EXCLUDE_TCP_PORTS="$SSH_PORT"
    [[ -n "$live_ssh" && "$live_ssh" != "$SSH_PORT" ]] && EXCLUDE_TCP_PORTS="${EXCLUDE_TCP_PORTS},${live_ssh}"
    EXCLUDE_UDP_PORTS=""
    echo ""
    ok "Every TCP/UDP port will be tunneled to the KHAREJ servers"
    info "SSH (${CYAN}${EXCLUDE_TCP_PORTS}${NC}) stays on this server."
    warn "Any panel/service running on THIS server (x-ui, nginx...) becomes"
    warn "unreachable unless you exclude its port below."
    echo ""
    echo -ne "    ${G2}❯${NC} Extra TCP ports to keep local [${CYAN}none${NC}]: "
    read -r input
    if [[ -n "$input" ]]; then
        while ! validate_ports "$input"; do
            err "Format: 2053 or 2053,8080 or 2000-2100"
            echo -ne "    ${G2}❯${NC} Extra TCP ports: "; read -r input
        done
        EXCLUDE_TCP_PORTS="${EXCLUDE_TCP_PORTS},${input}"
    fi
    echo -ne "    ${G2}❯${NC} Extra UDP ports to keep local [${CYAN}none${NC}]: "
    read -r input
    if [[ -n "$input" ]]; then
        while ! validate_ports "$input"; do
            err "Format: 53 or 53,5353"
            echo -ne "    ${G2}❯${NC} Extra UDP ports: "; read -r input
        done
        EXCLUDE_UDP_PORTS="$input"
    fi
}

ask_peer_ports() {
    local i="$1" input cur_t cur_u
    cur_t="$(pget "$i" TCP)"; cur_u="$(pget "$i" UDP)"
    # Only the FIRST server suggests 443. If every peer defaulted to 443 the
    # DNAT rules would all match the same port and only peer 1 would ever get
    # traffic - the classic "only one server works" trap. Peers 2+ start blank
    # so each is given its OWN distinct ports on purpose.
    [[ -z "$cur_t" && "$i" == "1" ]] && cur_t="$DEF_TCP_PORTS"
    [[ -z "$cur_u" && "$i" == "1" ]] && cur_u="$DEF_UDP_PORTS"
    SSH_PORT=$(detect_ssh_port)
    echo ""
    echo -e "    ${G6}▸${NC} Ports handled by ${WHITE}$(pget "$i" NAME)${NC} ${DIM}($(pget "$i" IP))${NC}"
    echo -ne "    ${G2}❯${NC} TCP ports [${CYAN}${cur_t}${NC}] ${DIM}(or 'none')${NC}: "
    read -r input
    input=${input:-$cur_t}
    [[ "$input" == "none" ]] && input=""
    while ! validate_ports "$input"; do
        err "Format: 443 or 443,8443 or 2000-3000"
        echo -ne "    ${G2}❯${NC} TCP ports: "; read -r input
    done
    if [[ -n "$input" && ",${input}," == *",${SSH_PORT},"* ]]; then
        warn "SSH port ${SSH_PORT} removed from this list for safety."
        input=$(echo "$input" | tr ',' '\n' | grep -vx "$SSH_PORT" | paste -sd, -)
    fi
    pset "$i" TCP "$input"

    echo -ne "    ${G2}❯${NC} UDP ports [${CYAN}${cur_u}${NC}] ${DIM}(or 'none')${NC}: "
    read -r input
    input=${input:-$cur_u}
    [[ "$input" == "none" ]] && input=""
    while ! validate_ports "$input"; do
        err "Format: 443 or 443,8443 or 2000-3000"
        echo -ne "    ${G2}❯${NC} UDP ports: "; read -r input
    done
    pset "$i" UDP "$input"
}

# balance mode: optionally pin ports that ONLY this server has. Empty = this
# server just shares the balanced load. Unlike ask_peer_ports, the default is
# always blank (pinning is the exception, not the rule).
ask_peer_pinned() {
    local i="$1" input
    echo -ne "    ${G2}❯${NC} Ports ONLY on ${WHITE}$(pget "$i" NAME)${NC} ${DIM}(Enter = none, e.g. 11283,55404)${NC}: "
    read -r input
    input="$(echo "$input" | tr -d ' ')"
    [[ "$input" == "none" ]] && input=""
    while [[ -n "$input" ]] && ! validate_ports "$input"; do
        err "Format: 11283 or 11283,55404 or 2000-3000 (or Enter for none)"
        echo -ne "    ${G2}❯${NC} Ports only on $(pget "$i" NAME): "; read -r input
        input="$(echo "$input" | tr -d ' ')"
    done
    # a pinned port must not also be an SSH/local-kept port
    if [[ -n "$input" && ",${input}," == *",${SSH_PORT:-22},"* ]]; then
        warn "SSH port removed from the pin list."
        input=$(echo "$input" | tr ',' '\n' | grep -vx "${SSH_PORT:-22}" | paste -sd, -)
    fi
    pset "$i" TCP "$input"
    pset "$i" UDP "$input"
}

ask_peer_weight() {
    local i="$1" input cur
    cur="$(pget "$i" WEIGHT)"; cur="${cur:-1}"
    echo -ne "    ${G2}❯${NC} Weight for ${WHITE}$(pget "$i" NAME)${NC} [${CYAN}${cur}${NC}] ${DIM}(share of new connections)${NC}: "
    read -r input
    if [[ "$input" =~ ^[0-9]+$ ]] && ((input >= 1 && input <= 100)); then
        pset "$i" WEIGHT "$input"
    else
        pset "$i" WEIGHT "$cur"
    fi
}

# how traffic is spread over the KHAREJ servers (IRAN only)
choose_balance_mode() {
    local input i
    echo ""
    echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
    echo -e "${G3}    │${NC}  ${G1}🔀${NC} ${WHITE}${BOLD}TRAFFIC DISTRIBUTION ACROSS KHAREJ SERVERS${NC}                ${G3}│${NC}"
    echo -e "${G3}    ├──────────────────────────────────────────────────────────────┤${NC}"
    echo -e "${G3}    │${NC}  ${G2}[1]${NC} ${WHITE}PORTS${NC}     each server owns its own ports ${GREEN}(default)${NC}     ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}e.g. 443 → Germany, 8443 → Finland, 2087 → Turkey.${NC}      ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}Every server works at the same time, independently.${NC}     ${G3}│${NC}"
    echo -e "${G3}    │${NC}  ${G2}[2]${NC} ${WHITE}BALANCE${NC}   all ports → spread over healthy peers        ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}For servers that carry the SAME configs. Each NEW${NC}       ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}connection picks a peer; established ones stay put.${NC}      ${G3}│${NC}"
    echo -e "${G3}    │${NC}  ${G2}[3]${NC} ${WHITE}FAILOVER${NC}  all ports → first HEALTHY peer only          ${G3}│${NC}"
    echo -e "${G3}    │${NC}      ${DIM}One server carries everything; the rest are spares.${NC}     ${G3}│${NC}"
    echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
    echo ""
    echo -ne "    ${G2}❯${NC} Distribution [${CYAN}1${NC}]: "
    read -r input
    case "${input:-1}" in
        2) BALANCE_MODE="balance" ;;
        3) BALANCE_MODE="failover" ;;
        *) BALANCE_MODE="ports" ;;
    esac
    # failover with several servers looks like "only one KHAREJ connects":
    # the others are up but only stand by. Make that impossible to miss.
    if [[ "$BALANCE_MODE" == "failover" ]] && ((PEER_COUNT > 1)); then
        echo ""
        warn "FAILOVER: only ${WHITE}$(pget 1 NAME)${NC} carries traffic; the other $((PEER_COUNT - 1)) server(s) only stand by."
        info "Each KHAREJ has its own configs/ports (x-ui panels)?  → choose ${WHITE}1 PORTS${NC}"
        info "All KHAREJ servers have the SAME configs?             → choose ${WHITE}2 BALANCE${NC}"
        echo -ne "    ${G2}❯${NC} Keep FAILOVER? [y/${CYAN}1${NC}/${CYAN}2${NC}]: "
        read -r input
        case "$input" in
            1) BALANCE_MODE="ports" ;;
            2) BALANCE_MODE="balance" ;;
        esac
    fi
    ok "Distribution: ${CYAN}$(balance_label "$BALANCE_MODE")${NC}"

    case "$BALANCE_MODE" in
        ports)
            info "Give each server its OWN ports - no two servers may share a port."
            while :; do
                for ((i = 1; i <= PEER_COUNT; i++)); do ask_peer_ports "$i"; done
                local clash
                clash="$(ports_collision)"
                [[ -z "$clash" ]] && break
                echo ""
                err "The same port is assigned to more than one server:"
                echo "$clash" | while IFS= read -r l; do echo -e "        ${RED}•${NC} ${l}"; done
                warn "Only the first server would receive that port. Re-enter the ports so each is unique."
                echo ""
            done
            EXCLUDE_TCP_PORTS=""; EXCLUDE_UDP_PORTS=""
            SSH_PORT=$(detect_ssh_port)
            ;;
        balance)
            echo ""
            info "New connections are spread across all servers on the SAME ports."
            info "Best when every server carries the same configs (identical x-ui)."
            info "Weights decide the share: 2 vs 1 means twice the connections."
            for ((i = 1; i <= PEER_COUNT; i++)); do ask_peer_weight "$i"; done
            echo ""
            info "If a server has ports NO OTHER server has, pin them so they are"
            info "never balanced away. Leave blank if all servers are identical."
            SSH_PORT=$(detect_ssh_port)
            while :; do
                for ((i = 1; i <= PEER_COUNT; i++)); do ask_peer_pinned "$i"; done
                local clash; clash="$(ports_collision)"
                [[ -z "$clash" ]] && break
                echo ""
                err "A pinned port is on more than one server (only the first would get it):"
                echo "$clash" | while IFS= read -r l; do echo -e "        ${RED}•${NC} ${l}"; done
                warn "Pinned ports must be unique to one server. Re-enter."
                echo ""
            done
            ask_local_exclusions
            ;;
        *)
            info "Priority = peer order. Use menu 6 to reorder."
            ask_local_exclusions
            ;;
    esac
}

# ──────────────────── IP INPUT (per role) ────────────────────

print_plan() {
    local i
    echo ""
    echo -e "${G3}    ┌────────────────────────────────────────────────────────────┐${NC}"
    printf "    ${G3}│${NC}  Role           : ${CYAN}%-38s${NC}${G3}│${NC}\n" "$ROLE"
    printf "    ${G3}│${NC}  IRAN Public    : ${CYAN}%-38s${NC}${G3}│${NC}\n" "$IRAN_IP"
    printf "    ${G3}│${NC}  Transport      : ${CYAN}%-38s${NC}${G3}│${NC}\n" "$(mode_label "$TUNNEL_MODE")"
    printf "    ${G3}│${NC}  MTU            : ${CYAN}%-38s${NC}${G3}│${NC}\n" "$TUNNEL_MTU"
    if [[ "$ROLE" == "IRAN" ]]; then
    printf "    ${G3}│${NC}  Distribution   : ${CYAN}%-38s${NC}${G3}│${NC}\n" "$(balance_label "$BALANCE_MODE")"
    printf "    ${G3}│${NC}  KHAREJ servers : ${CYAN}%-38s${NC}${G3}│${NC}\n" "$PEER_COUNT"
    fi
    echo -e "${G3}    ├────────────────────────────────────────────────────────────┤${NC}"
    for ((i = 1; i <= PEER_COUNT; i++)); do
        printf "    ${G3}│${NC}  ${G2}%d.${NC} %-10s ${CYAN}%-15s${NC} ${DIM}%s ↔ %s${NC}%*s${G3}│${NC}\n" \
            "$i" "$(pget "$i" NAME)" "$(pget "$i" IP)" "$(pget "$i" LOCAL)" "$(pget "$i" REMOTE)" 3 ""
        case "$BALANCE_MODE" in
            ports)
    printf "    ${G3}│${NC}     ${DIM}tcp${NC} ${CYAN}%-18s${NC} ${DIM}udp${NC} ${CYAN}%-19s${NC}${G3}│${NC}\n" \
            "$(pget "$i" TCP)" "$(pget "$i" UDP)" ;;
            balance)
    printf "    ${G3}│${NC}     ${DIM}weight${NC} ${CYAN}%-40s${NC} ${G3}│${NC}\n" "$(pget "$i" WEIGHT)"
    [[ -n "$(pget "$i" TCP)" ]] && \
    printf "    ${G3}│${NC}     ${DIM}pinned ports${NC} ${CYAN}%-34s${NC} ${G3}│${NC}\n" "$(pget "$i" TCP)" ;;
        esac
        [[ "$TUNNEL_MODE" == "fou" ]] && \
    printf "    ${G3}│${NC}     ${DIM}fou udp${NC} ${CYAN}%-39s${NC}${G3}│${NC}\n" "$(pget "$i" FOU)"
    done
    echo -e "${G3}    ├────────────────────────────────────────────────────────────┤${NC}"
    if [[ "$ROLE" == "IRAN" && "$BALANCE_MODE" != "ports" ]]; then
    printf "    ${G3}│${NC}  Kept local TCP : ${GREEN}%-38s${NC}${G3}│${NC}\n" "${EXCLUDE_TCP_PORTS:-none}"
    printf "    ${G3}│${NC}  Kept local UDP : ${GREEN}%-38s${NC}${G3}│${NC}\n" "${EXCLUDE_UDP_PORTS:-none}"
    fi
    printf "    ${G3}│${NC}  SSH protected  : ${GREEN}%-38s${NC}${G3}│${NC}\n" "port ${SSH_PORT} (never tunneled)"
    echo -e "${G3}    └────────────────────────────────────────────────────────────┘${NC}"
}

# IRAN: one local IP, then as many KHAREJ servers as the user wants
input_iran_config() {
    local auto_ip input ip name idx c
    auto_ip=$(detect_local_ip)
    ROLE="IRAN"
    clear_peers
    PRIV_BASE="$DEF_PRIV_BASE"

    echo ""
    echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
    echo -e "${G3}    │${NC}  ${G1}🔧${NC} ${WHITE}${BOLD}IP CONFIGURATION${NC}                                          ${G3}│${NC}"
    echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
    echo ""
    echo -ne "    ${G2}❯${NC} IRAN Public IP [${CYAN}${auto_ip}${NC}]: "
    read -r input
    IRAN_IP=${input:-$auto_ip}
    while ! validate_ip "$IRAN_IP"; do
        err "Invalid!"; echo -ne "    ${G2}❯${NC} IRAN IP: "; read -r IRAN_IP
    done
    SELF_IP="$IRAN_IP"
    KHAREJ_IP=""
    ok "IRAN: ${CYAN}$IRAN_IP${NC}"

    echo ""
    info "Now add every KHAREJ server. Press Enter on an empty line when done."
    info "Each one gets its own tunnel, its own /30 and its own slot number."
    while ((PEER_COUNT < MAX_PEERS)); do
        echo ""
        echo -ne "    ${G2}❯${NC} KHAREJ #$((PEER_COUNT + 1)) Public IP ${DIM}(Enter = finish)${NC}: "
        read -r ip
        [[ -z "$ip" ]] && { ((PEER_COUNT > 0)) && break; err "At least one KHAREJ server is required."; continue; }
        if ! validate_ip "$ip"; then err "Invalid IP."; continue; fi
        if [[ "$ip" == "$IRAN_IP" ]]; then err "That is the IRAN server itself."; continue; fi
        if peer_ip_used "$ip"; then err "Already added."; continue; fi
        echo -ne "    ${G2}❯${NC} Label for ${CYAN}${ip}${NC} [${CYAN}kharej$((PEER_COUNT + 1))${NC}]: "
        read -r name
        name="${name:-kharej$((PEER_COUNT + 1))}"
        name=$(echo "$name" | tr -cd '[:alnum:]_-' | cut -c1-14)
        idx=$(next_free_index)
        add_peer "$ip" "$name" "$idx"
        ok "Added ${WHITE}${name}${NC} → ${CYAN}${ip}${NC}  ${DIM}slot ${idx}, iface ${TUNNEL_BASE}${idx}, $(slot_iran_ip "$idx") ↔ $(slot_kharej_ip "$idx")${NC}"
        KHAREJ_IP="$ip"   # kept for backward-compatible status output
    done

    choose_mode
    IKE_PORT=$(derive_num "midos-ike|${IRAN_IP}" 20000 60000)
    choose_balance_mode

    print_plan
    echo ""
    echo -ne "    ${G2}❯${NC} Confirm? [${GREEN}Y${NC}/n]: "
    read -r c
    [[ "$c" == "n" || "$c" == "N" ]] && return 1
    return 0
}

# KHAREJ: exactly one peer (the IRAN server) - it only needs to know which
# slot IRAN gave it, because the slot decides the /30 and the interface name.
input_kharej_config() {
    local auto_ip input c
    auto_ip=$(detect_local_ip)
    ROLE="KHAREJ"
    clear_peers
    PRIV_BASE="$DEF_PRIV_BASE"

    echo ""
    echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
    echo -e "${G3}    │${NC}  ${G1}🔧${NC} ${WHITE}${BOLD}IP CONFIGURATION${NC}                                          ${G3}│${NC}"
    echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
    echo ""
    echo -ne "    ${G2}❯${NC} KHAREJ Public IP (this server) [${CYAN}${auto_ip}${NC}]: "
    read -r input
    KHAREJ_IP=${input:-$auto_ip}
    while ! validate_ip "$KHAREJ_IP"; do
        err "Invalid!"; echo -ne "    ${G2}❯${NC} KHAREJ IP: "; read -r KHAREJ_IP
    done
    SELF_IP="$KHAREJ_IP"
    ok "KHAREJ: ${CYAN}$KHAREJ_IP${NC}"

    echo ""
    echo -ne "    ${G2}❯${NC} IRAN Public IP: "
    read -r IRAN_IP
    while ! validate_ip "$IRAN_IP"; do
        err "Invalid!"; echo -ne "    ${G2}❯${NC} IRAN IP: "; read -r IRAN_IP
    done
    ok "IRAN: ${CYAN}$IRAN_IP${NC}"

    choose_mode

    # The slot decides the /30 and the ESP SPIs, so two KHAREJ servers on the
    # same slot knock each other out and only one of them ever works. Letting
    # every server silently default to slot 1 was exactly that trap, so the
    # default is now to ask IRAN which slot it gave us (see autodetect_slot).
    echo ""
    info "The IRAN server gave every KHAREJ server its own ${WHITE}slot number${NC}."
    info "Slot 1 is ${CYAN}$(slot_iran_ip 1) ↔ $(slot_kharej_ip 1)${NC}, slot 2 is ${CYAN}$(slot_iran_ip 2) ↔ $(slot_kharej_ip 2)${NC}, ..."
    warn "Two KHAREJ servers must NEVER share the same slot."
    SLOT_AUTO=0
    if [[ "$TUNNEL_MODE" == "ipsec" ]]; then
        # IKE needs the slot before strongswan starts - no probing possible
        echo -ne "    ${G2}❯${NC} Slot number for THIS server (see the IRAN server list): "
        read -r input
    else
        info "Press Enter to detect it automatically (IRAN must already be set up)."
        echo -ne "    ${G2}❯${NC} Slot number for THIS server [${CYAN}auto${NC}]: "
        read -r input
        if [[ -z "$input" || "$input" == "auto" || "$input" == "a" ]]; then
            SLOT_AUTO=1; input=1
        fi
    fi
    PEER_INDEX="$input"
    while ! [[ "$PEER_INDEX" =~ ^[0-9]+$ ]] || ((PEER_INDEX < 1 || PEER_INDEX > MAX_PEERS)); do
        err "Slot must be 1..${MAX_PEERS}"
        echo -ne "    ${G2}❯${NC} Slot: "; read -r PEER_INDEX
    done
    add_peer "$IRAN_IP" "iran" "$PEER_INDEX"
    if ((SLOT_AUTO)); then
        ok "Slot: ${CYAN}auto${NC} ${DIM}(detected right after the install)${NC}"
    else
        ok "Slot ${CYAN}${PEER_INDEX}${NC} → iface ${CYAN}${TUNNEL_BASE}${PEER_INDEX}${NC}, me=${CYAN}$(slot_kharej_ip "$PEER_INDEX")${NC} iran=${CYAN}$(slot_iran_ip "$PEER_INDEX")${NC}"
    fi

    IKE_PORT=$(derive_num "midos-ike|${IRAN_IP}" 20000 60000)
    # A KHAREJ server never distributes anything - IRAN decides that. Pinning
    # "failover" here only made every KHAREJ status screen claim FAILOVER.
    BALANCE_MODE="$DEF_BALANCE_MODE"
    SSH_PORT=$(detect_ssh_port)
    EXCLUDE_TCP_PORTS="$SSH_PORT"

    print_plan
    echo ""
    echo -ne "    ${G2}❯${NC} Confirm? [${GREEN}Y${NC}/n]: "
    read -r c
    [[ "$c" == "n" || "$c" == "N" ]] && return 1
    return 0
}
# ──────────────────── KERNEL / NETWORK TUNING ────────────────────

write_sysctl() {
    cat > "$SYSCTL_FILE" << EOF
# Midos Tunnel - performance & tunnel-correctness tuning
# ---- forwarding (required for the tunnel to route anything) ----
net.ipv4.ip_forward = 1
net.ipv4.conf.all.forwarding = 1

# ---- reverse-path filter OFF: asymmetric tunnel paths are normal here.
#      strict rp_filter silently drops tunnelled return traffic.
net.ipv4.conf.all.rp_filter = 0
net.ipv4.conf.default.rp_filter = 0

# ---- congestion control: BBR + fq gives far lower loss/latency on
#      lossy international links than the default cubic+pfifo_fast
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr

# ---- socket buffers sized for high BDP (long fat pipe IR<->EU) ----
net.core.rmem_max = 67108864
net.core.wmem_max = 67108864
net.core.rmem_default = 1048576
net.core.wmem_default = 1048576
net.core.optmem_max = 65536
net.ipv4.tcp_rmem = 4096 1048576 67108864
net.ipv4.tcp_wmem = 4096 1048576 67108864
net.ipv4.udp_rmem_min = 16384
net.ipv4.udp_wmem_min = 16384

# ---- queueing / backlog: fewer drops during bursts ----
net.core.netdev_max_backlog = 30000
net.core.netdev_budget = 600
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 65535

# ---- TCP behaviour tuned for a tunnelled, sometimes lossy path ----
net.ipv4.tcp_mtu_probing = 1
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_fastopen = 3
net.ipv4.tcp_sack = 1
net.ipv4.tcp_fack = 1
net.ipv4.tcp_window_scaling = 1
net.ipv4.tcp_timestamps = 1
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_keepalive_time = 300
net.ipv4.tcp_keepalive_intvl = 30
net.ipv4.tcp_keepalive_probes = 5
# 12 (~100s) reclaims dead connections far sooner than the default 15 (~15min)
# without being trigger-happy: a loss burst on an international path must never
# be mistaken for a dead peer, or live streams get torn down mid-playback.
net.ipv4.tcp_retries2 = 12
net.ipv4.tcp_max_tw_buckets = 1440000
net.ipv4.tcp_tw_reuse = 1
net.ipv4.ip_local_port_range = 1024 65535

# ---- conntrack: a busy relay exhausts the default table and starts
#      dropping NEW connections ("random" packet loss under load)
net.netfilter.nf_conntrack_max = 1048576
net.netfilter.nf_conntrack_tcp_be_liberal = 1
net.netfilter.nf_conntrack_tcp_timeout_established = 7440
# QUIC carries YouTube/Instagram video. A player that has buffered 30-60s goes
# silent on the wire for that long; with the 60s/180s defaults the NAT mapping
# expires mid-playback, the next chunk request arrives from a new port and the
# stream has to be rebuilt - which the viewer sees as a short freeze.
net.netfilter.nf_conntrack_udp_timeout = 120
net.netfilter.nf_conntrack_udp_timeout_stream = 600

# ---- misc ----
net.ipv4.icmp_echo_ignore_broadcasts = 1
net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
fs.file-max = 2097152
EOF
    modprobe tcp_bbr 2>/dev/null || true
    modprobe nf_conntrack 2>/dev/null || true
    echo "tcp_bbr" > /etc/modules-load.d/midos-bbr.conf
    sysctl -p "$SYSCTL_FILE" > /dev/null 2>&1 || true
    # clean up duplicate/conflicting legacy entries
    sed -i '/net.ipv4.ip_forward/d;/net.ipv4.conf.all.rp_filter/d' /etc/sysctl.conf 2>/dev/null || true
    echo "net.ipv4.ip_forward=1" >> /etc/sysctl.conf
}

install_deps() {
    export DEBIAN_FRONTEND=noninteractive
    local pkgs="iptables iproute2 iputils-ping"
    [[ "$TUNNEL_MODE" == "ipsec" ]] && pkgs="$pkgs strongswan strongswan-starter libstrongswan-standard-plugins"
    apt-get update -qq > /dev/null 2>&1
    # shellcheck disable=SC2086
    apt-get install -y -qq $pkgs > /dev/null 2>&1
    apt-get install -y -qq iptables-persistent netfilter-persistent > /dev/null 2>&1
}

# ──────────────────── IPSEC (strongswan) ────────────────────

# One transport-mode conn per peer, all protecting only the MOI flow.
write_ipsec_config() {
    local i local_pub remote_pub
    [[ -d /etc/ipsec.d ]] || mkdir -p /etc/ipsec.d
    local_pub="$SELF_IP"

    {
        echo "# Midos Tunnel - IPsec transport mode protecting MOI (proto 47)"
        echo "config setup"
        echo "    charondebug=\"ike 0, knl 0, cfg 0\""
        echo "    uniqueids=no"
        for ((i = 1; i <= PEER_COUNT; i++)); do
            remote_pub="$(pget "$i" IP)"
            echo ""
            echo "conn midos$(pget "$i" IDX)"
            echo "    left=${local_pub}"
            echo "    leftprotoport=gre"
            echo "    right=${remote_pub}"
            echo "    rightprotoport=gre"
            echo "    type=transport"
            echo "    authby=secret"
            echo "    keyexchange=ikev2"
            echo "    ike=aes256gcm16-prfsha384-ecp384,aes256-sha256-modp2048!"
            echo "    esp=aes256gcm16-ecp384,aes256-sha256-modp2048!"
            # forceencaps: always wrap ESP inside UDP so middleboxes/DPI only
            # ever see an opaque UDP flow instead of IP proto 50
            echo "    forceencaps=yes"
            echo "    dpdaction=restart"
            echo "    dpddelay=20s"
            echo "    dpdtimeout=90s"
            echo "    closeaction=restart"
            echo "    keyingtries=%forever"
            echo "    ikelifetime=8h"
            echo "    lifetime=1h"
            echo "    auto=start"
        done
    } > /etc/ipsec.conf
    chmod 644 /etc/ipsec.conf

    {
        for ((i = 1; i <= PEER_COUNT; i++)); do
            echo "${local_pub} $(pget "$i" IP) : PSK \"${IPSEC_PSK}\""
        done
    } > /etc/ipsec.secrets
    chmod 600 /etc/ipsec.secrets

    # move IKE off the well-known 500/4500 ports; one NAT-T port serves every
    # conn, so it is derived from the IRAN IP alone and matches on all peers
    IKE_PORT=$(derive_num "midos-ike|${IRAN_IP}" 20000 60000)
    if [[ -f /etc/strongswan.conf ]]; then
        cat > /etc/strongswan.conf << EOF
charon {
    load_modular = yes
    # port 0 disables plain IKE/500 entirely; everything runs UDP-encapsulated
    port = 0
    port_nat_t = ${IKE_PORT}
    install_routes = no
    install_virtual_ip = no
    retransmit_timeout = 2.0
    retransmit_tries = 8
    plugins {
        include strongswan.d/charon/*.conf
    }
}
include strongswan.d/*.conf
EOF
    fi
}
ipsec_service_name() {
    if systemctl list-unit-files 2>/dev/null | grep -q '^strongswan-starter\.service'; then
        echo "strongswan-starter"
    elif systemctl list-unit-files 2>/dev/null | grep -q '^strongswan\.service'; then
        echo "strongswan"
    else
        echo ""
    fi
}

# ──────────────────── GENERATED SCRIPTS ────────────────────

write_up_script() {
    cat > "$UP_SCRIPT" << 'UPEOF'
#!/bin/bash
# Midos Tunnel - bring every peer tunnel + firewall + tuning up (idempotent)
#   Midos-tunnel-up.sh          -> full rebuild (all peers + firewall)
#   Midos-tunnel-up.sh <n>      -> rebuild ONLY peer n, leave the rest running
CONFIG_FILE="/etc/Midos-tunnel/config"
LOG_FILE="/var/log/Midos-tunnel.log"
NAT_SCRIPT="/usr/local/sbin/Midos-tunnel-nat.sh"
[[ -f "$CONFIG_FILE" ]] || { echo "no config"; exit 1; }
# shellcheck disable=SC1090
source "$CONFIG_FILE"

# Turn the chosen tuning profile into concrete queue settings. Only the qdisc
# differs; the shaper itself stays opt-in, because a rate that does not match
# the real bottleneck hurts more than no shaping at all.
tuning_qdisc_args() {
    case "${TUNING_PROFILE:-balanced}" in
        low_latency) echo "fq_codel target 3ms interval 60ms quantum 300" ;;
        throughput)  echo "fq_codel target 10ms interval 150ms" ;;
        *)           echo "fq_codel" ;;
    esac
}

# one writer at a time: a full restart, the watchdog's single-peer repair and
# the rekey timer must never rebuild the same SA set concurrently
exec 9> /run/Midos-tunnel.lock
flock 9

MIDOS_PRE="MIDOS_PRE"; MIDOS_POST="MIDOS_POST"; MIDOS_FWD="MIDOS_FWD"; MIDOS_IN="MIDOS_IN"
MIDOS_MARK="MIDOS_MARK"
TUNNEL_BASE="${TUNNEL_BASE:-midosMOI}"
ONLY_PEER="$1"
log() { echo "$(date '+%F %T') [up] $*" >> "$LOG_FILE"; }
pget() { local v="PEER_${1}_${2}"; echo "${!v}"; }

if [[ "$ROLE" == "IRAN" ]]; then SELF_IP="${SELF_IP:-$IRAN_IP}"; else SELF_IP="${SELF_IP:-$KHAREJ_IP}"; fi
WAN_IF=$(ip -4 route get 8.8.8.8 2>/dev/null | grep -oP 'dev \K\S+' | head -1)
[[ -z "$WAN_IF" ]] && WAN_IF=$(ip -4 route show default | awk '/default/{print $5; exit}')

# ---------- modules ----------
modprobe ip_gre 2>/dev/null
modprobe gre 2>/dev/null
[[ "$TUNNEL_MODE" == "fou" ]] && modprobe fou 2>/dev/null
[[ "$TUNNEL_MODE" == "ipsec" || "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" ]] && { modprobe esp4 2>/dev/null; modprobe xfrm4_tunnel 2>/dev/null; modprobe xfrm_user 2>/dev/null; }
modprobe tcp_bbr 2>/dev/null
modprobe nf_conntrack 2>/dev/null

# encapsulation overhead the shaper has to account for
case "$TUNNEL_MODE" in
    esp)   TC_OVERHEAD=64 ;;
    fou)   TC_OVERHEAD=36 ;;
    ipsec) TC_OVERHEAD=90 ;;
    *)     TC_OVERHEAD=28 ;;
esac

# ---------- ESP key schedule ----------
# Nothing on the wire stays constant. The passphrase goes through PBKDF2
# (600k rounds, so a guessed passphrase can no longer be tested against
# captured traffic at hash speed) into a per-deployment master key; from it
# every peer gets its OWN keys AND its OWN SPIs for every epoch of
# ESP_ROTATE_SEC, so the flow rekeys itself the way an IKE-driven VPN does.
# Both ends only need the same passphrase and clocks within ESP_WIN epochs.
ESP_ROTATE_SEC="${ESP_ROTATE_SEC:-3600}"
ESP_TFC_PAD="${ESP_TFC_PAD:-0}"
ESP_WIN=2                       # inbound SAs are kept for epochs cur-2 .. cur+2
ESP_MASTER=""
ESP_MASTER_FILE="/etc/Midos-tunnel/esp.master"

esp_epoch() {
    if (( ESP_ROTATE_SEC > 0 )); then echo $(( $(date +%s) / ESP_ROTATE_SEC )); else echo 0; fi
}

# the epochs we accept right now
esp_epochs_in() {
    local cur e
    cur=$(esp_epoch)
    if (( ESP_ROTATE_SEC > 0 )); then
        for ((e = cur - ESP_WIN; e <= cur + ESP_WIN; e++)); do echo "$e"; done
    else
        echo 0
    fi
}

# PBKDF2 once per passphrase; cached root-only next to the config (which
# already holds the passphrase itself), tagged so a changed passphrase
# invalidates it
esp_master_load() {
    local salt tag line
    [[ -n "$ESP_MASTER" ]] && return 0
    salt=$(printf '%s' "midos-esp-salt|${IRAN_IP}" | sha256sum | cut -c1-16)
    tag=$(printf '%s|%s' "$IPSEC_PSK" "$salt" | sha256sum | cut -c1-16)
    if [[ -r "$ESP_MASTER_FILE" ]]; then
        line=$(cat "$ESP_MASTER_FILE" 2>/dev/null)
        if [[ "${line%% *}" == "$tag" ]]; then
            ESP_MASTER="${line##* }"
            [[ ${#ESP_MASTER} -eq 64 ]] && return 0
        fi
    fi
    ESP_MASTER=$(printf '%s\n' "$IPSEC_PSK" | openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -md sha256 \
        -S "$salt" -pass stdin -P 2>/dev/null | awk -F= '/^key=/{print tolower($2)}')
    [[ ${#ESP_MASTER} -eq 64 ]] || { ESP_MASTER=""; return 1; }
    ( umask 077; printf '%s %s\n' "$tag" "$ESP_MASTER" > "${ESP_MASTER_FILE}.tmp" && mv -f "${ESP_MASTER_FILE}.tmp" "$ESP_MASTER_FILE" )
    return 0
}

# esp_mat <iran-ip> <kharej-ip> <epoch> -> K_I2K K_K2I SPI_I2K SPI_K2I
# (rfc4106 wants 32 key bytes + 4 salt bytes = 72 hex; SPIs below 256 are reserved)
esp_mat() {
    local b="${ESP_MASTER}|${1}|${2}|${3}" n
    K_I2K="0x$(printf '%s' "${b}|key-i2k" | sha512sum | cut -c1-72)"
    K_K2I="0x$(printf '%s' "${b}|key-k2i" | sha512sum | cut -c1-72)"
    n=$(( 0x$(printf '%s' "${b}|spi-i2k" | sha256sum | cut -c1-8) )); (( n < 256 )) && n=$(( n + 256 ))
    printf -v SPI_I2K '0x%08x' "$n"
    n=$(( 0x$(printf '%s' "${b}|spi-k2i" | sha256sum | cut -c1-8) )); (( n < 256 )) && n=$(( n + 256 ))
    printf -v SPI_K2I '0x%08x' "$n"
}

# esp_peer_vars <i> -> LPUB RPUB IR KH IDX
esp_peer_vars() {
    IDX="$(pget "$1" IDX)"; IR="$IRAN_IP"
    if [[ "$ROLE" == "IRAN" ]]; then LPUB="$IRAN_IP"; RPUB="$(pget "$1" IP)"; KH="$RPUB"
    else LPUB="$SELF_IP"; RPUB="$IRAN_IP"; KH="$SELF_IP"; fi
}
esp_spi_in()  { esp_peer_vars "$1"; esp_mat "$IR" "$KH" "$2"; [[ "$ROLE" == "IRAN" ]] && echo "$SPI_K2I" || echo "$SPI_I2K"; }
esp_spi_out() { esp_peer_vars "$1"; esp_mat "$IR" "$KH" "$2"; [[ "$ROLE" == "IRAN" ]] && echo "$SPI_I2K" || echo "$SPI_K2I"; }

# esp_state_add <i> <epoch> <in|out>
esp_state_add() {
    local i="$1" e="$2" dir="$3" kout kin spiout spiin err pad="" encap=""
    # espudp: the same SA, but the kernel wraps every ESP packet in UDP so
    # the outer packet is an ordinary datagram instead of IP protocol 50.
    if [[ "$TUNNEL_MODE" == "espudp" ]]; then
        local uport; uport="$(pget "$i" UDPPORT)"
        [[ -n "$uport" ]] && encap="encap espinudp $uport $uport 0.0.0.0"
    fi
    esp_peer_vars "$i"; esp_mat "$IR" "$KH" "$e"
    if [[ "$ROLE" == "IRAN" ]]; then kout="$K_I2K"; kin="$K_K2I"; spiout="$SPI_I2K"; spiin="$SPI_K2I"
    else kout="$K_K2I"; kin="$K_I2K"; spiout="$SPI_K2I"; spiin="$SPI_I2K"; fi
    (( ESP_TFC_PAD > 0 )) && pad="tfcpad $ESP_TFC_PAD"
    # NO anti-replay window, deliberately: without IKE, either end rebuilding
    # its SA restarts the sequence at 0, and a peer holding a replay window
    # would then drop everything until the next epoch. GCM still authenticates
    # every packet; the rotation bounds what a replayed one could ever do.
    if [[ "$dir" == "out" ]]; then
        err=$(ip xfrm state add src "$LPUB" dst "$RPUB" proto esp spi "$spiout" reqid $((100 + IDX)) \
            mode transport aead 'rfc4106(gcm(aes))' "$kout" 128 $encap \
            sel src "$LPUB" dst "$RPUB" proto gre $pad 2>&1)
    else
        err=$(ip xfrm state add src "$RPUB" dst "$LPUB" proto esp spi "$spiin" reqid $((200 + IDX)) \
            mode transport aead 'rfc4106(gcm(aes))' "$kin" 128 $encap \
            sel src "$RPUB" dst "$LPUB" proto gre 2>&1)
    fi
    [[ -n "$err" && "$err" != *xist* ]] && log "peer $i esp ${dir} epoch ${e}: $err"
    return 0
}

# ---------- one peer down ----------
peer_link_down() {
    local i="$1" ifc fou lpub rpub
    ifc="$(pget "$i" IFACE)"; fou="$(pget "$i" FOU)"
    if [[ "$ROLE" == "IRAN" ]]; then lpub="$IRAN_IP"; rpub="$(pget "$i" IP)"
    else lpub="$SELF_IP"; rpub="$IRAN_IP"; fi
    if [[ "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" ]]; then
        # remove only OUR entries - never flush, other IPsec on the box must survive
        ip xfrm state deleteall src "$lpub" dst "$rpub" proto esp 2>/dev/null
        ip xfrm state deleteall src "$rpub" dst "$lpub" proto esp 2>/dev/null
        ip xfrm policy delete src "$lpub" dst "$rpub" proto gre dir out 2>/dev/null
        ip xfrm policy delete src "$rpub" dst "$lpub" proto gre dir in  2>/dev/null
        ip xfrm policy delete src "$rpub" dst "$lpub" proto gre dir fwd 2>/dev/null
    fi
    ip link set "$ifc" down 2>/dev/null
    ip link del "$ifc" 2>/dev/null
    ip tunnel del "$ifc" 2>/dev/null
    [[ -n "$fou" && "$TUNNEL_MODE" == "fou" ]] && ip fou del port "$fou" ipproto 47 2>/dev/null
    return 0
}

# ---------- one peer up ----------
peer_link_up() {
    local i="$1"
    local ifc rpub lpub lpriv rpriv key fou idx
    ifc="$(pget "$i" IFACE)"; key="$(pget "$i" KEY)"; fou="$(pget "$i" FOU)"; idx="$(pget "$i" IDX)"
    lpriv="$(pget "$i" LOCAL)"; rpriv="$(pget "$i" REMOTE)"
    if [[ "$ROLE" == "IRAN" ]]; then lpub="$IRAN_IP"; rpub="$(pget "$i" IP)"
    else lpub="$SELF_IP"; rpub="$IRAN_IP"; fi

    peer_link_down "$i"
    if [[ "$(pget "$i" ENABLED)" != "1" ]]; then
        log "peer $i ($(pget "$i" NAME)) disabled - left down"
        return 0
    fi

    case "$TUNNEL_MODE" in
        fou)
            # FOU listener: decapsulate UDP:<port> back into MOI. Two peers that
            # happen to derive the same port share one listener - harmless,
            # the MOI endpoints still separate the flows.
            ip fou add port "$fou" ipproto 47 2>/dev/null
            # encap-csum keeps a real UDP checksum, which some middleboxes require;
            # older iproute2/kernels reject the flag, so retry without it.
            ip link add name "$ifc" type gre \
                local "$lpub" remote "$rpub" ttl 64 key "$key" \
                encap fou encap-sport "$fou" encap-dport "$fou" \
                encap-csum 2>>"$LOG_FILE" \
            || ip link add name "$ifc" type gre \
                local "$lpub" remote "$rpub" ttl 64 key "$key" \
                encap fou encap-sport "$fou" encap-dport "$fou" 2>>"$LOG_FILE"
            ;;
        *)
            ip link add name "$ifc" type gre \
                local "$lpub" remote "$rpub" ttl 64 key "$key" 2>>"$LOG_FILE"
            ;;
    esac

    if ! ip link show "$ifc" > /dev/null 2>&1; then
        log "ERROR: could not create $ifc (peer $i, mode=$TUNNEL_MODE)"
        return 1
    fi

    # ---- IPsec ESP (mode=esp): AES-256-GCM transport mode around this peer's
    # MOI flow, keys and SPIs from the rotating schedule above. Installed
    # BEFORE the link comes up so not a single MOI packet leaves in the clear;
    # the inbound policy also makes the kernel refuse any MOI packet from the
    # peer that did not arrive inside authenticated ESP - nothing can be
    # injected into the tunnel from outside.
    if [[ ( "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" ) && -n "$IPSEC_PSK" ]]; then
        local e
        if ! esp_master_load; then
            log "ERROR: cannot derive ESP keys (openssl missing?) - peer $i kept DOWN rather than unencrypted"
            peer_link_down "$i"
            return 1
        fi
        ip xfrm policy add src "$lpub" dst "$rpub" proto gre dir out \
            tmpl src "$lpub" dst "$rpub" proto esp reqid $((100 + idx)) mode transport 2>>"$LOG_FILE"
        ip xfrm policy add src "$rpub" dst "$lpub" proto gre dir in \
            tmpl src "$rpub" dst "$lpub" proto esp reqid $((200 + idx)) mode transport 2>>"$LOG_FILE"
        ip xfrm policy add src "$rpub" dst "$lpub" proto gre dir fwd \
            tmpl src "$rpub" dst "$lpub" proto esp reqid $((200 + idx)) mode transport 2>>"$LOG_FILE"
        for e in $(esp_epochs_in); do esp_state_add "$i" "$e" in; done
        esp_state_add "$i" "$(esp_epoch)" out
    fi

    ip link set dev "$ifc" mtu "$TUNNEL_MTU"
    ip link set dev "$ifc" txqueuelen 1000
    ip addr flush dev "$ifc" 2>/dev/null
    ip addr add "${lpriv}/30" dev "$ifc"
    ip link set dev "$ifc" up

    # Anti-bufferbloat. fq_codel alone only helps if the queue actually forms
    # here; when the bottleneck is further down the path the queue builds in the
    # ISP buffer instead and latency under load triples. Shaping just below the
    # real bottleneck pulls the queue back into cake, which then keeps every
    # flow near idle latency. The budget is per peer: each tunnel is its own path.
    if [[ "${SHAPE_MBIT:-0}" -gt 0 ]] && tc qdisc replace dev "$ifc" root cake \
            bandwidth "${SHAPE_MBIT}mbit" besteffort triple-isolate nat \
            overhead $TC_OVERHEAD 2>/dev/null; then
        :
    else
        # shellcheck disable=SC2046  # the profile deliberately expands to args
        tc qdisc replace dev "$ifc" root $(tuning_qdisc_args) 2>/dev/null ||
            tc qdisc replace dev "$ifc" root fq_codel 2>/dev/null
    fi

    # per-interface knobs (must be set AFTER the iface exists)
    sysctl -qw "net.ipv4.conf.${ifc}.rp_filter=0"    2>/dev/null
    sysctl -qw "net.ipv4.conf.${ifc}.forwarding=1"   2>/dev/null
    sysctl -qw "net.ipv4.conf.${ifc}.accept_local=1" 2>/dev/null

    log "peer $i ($(pget "$i" NAME)) up: $ifc $lpriv <-> $rpriv via $rpub"
    return 0
}

# ---------- rekey (systemd timer, esp mode) ----------
# Slide the ESP window: add the SAs for epochs that came into view, drop the
# ones that fell out, move outbound to the current epoch. Adding before
# deleting means the peer never sees a gap - it already holds our next SPI,
# because both ends install cur+1 / cur+2 a whole epoch ahead of time.
if [[ "$ONLY_PEER" == "rekey" ]]; then
    [[ ( "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" ) && -n "$IPSEC_PSK" && "$ESP_ROTATE_SEC" -gt 0 ]] || exit 0
    esp_master_load || { log "rekey: cannot derive ESP keys"; exit 1; }
    cur=$(esp_epoch); changed=0
    for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
        [[ "$(pget "$i" ENABLED)" == "1" ]] || continue
        ip link show "$(pget "$i" IFACE)" > /dev/null 2>&1 || continue      # the watchdog's job
        esp_peer_vars "$i"
        want_in=""
        for e in $(esp_epochs_in); do want_in="$want_in $(esp_spi_in "$i" "$e")"; done
        want_out=$(esp_spi_out "$i" "$cur")
        have_in=$(ip xfrm state list src "$RPUB" dst "$LPUB" proto esp 2>/dev/null | awk '$1=="proto"&&$2=="esp"{print $4}' | tr '\n' ' ')
        have_out=$(ip xfrm state list src "$LPUB" dst "$RPUB" proto esp 2>/dev/null | awk '$1=="proto"&&$2=="esp"{print $4}' | tr '\n' ' ')
        for e in $(esp_epochs_in); do
            s=$(esp_spi_in "$i" "$e")
            [[ " $have_in " == *" $s "* ]] || { esp_state_add "$i" "$e" in; changed=1; }
        done
        [[ " $have_out " == *" $want_out "* ]] || { esp_state_add "$i" "$cur" out; changed=1; }
        for s in $have_out; do
            [[ "$s" == "$want_out" ]] || { ip xfrm state delete src "$LPUB" dst "$RPUB" proto esp spi "$s" 2>/dev/null; changed=1; }
        done
        for s in $have_in; do
            [[ " $want_in " == *" $s "* ]] || { ip xfrm state delete src "$RPUB" dst "$LPUB" proto esp spi "$s" 2>/dev/null; changed=1; }
        done
    done
    (( changed )) && log "rekey: epoch ${cur} in effect (inbound window ±${ESP_WIN})"
    exit 0
fi

# ---------- single-peer rebuild (watchdog fast path) ----------
# Rebuilding one dead tunnel must not disturb the peers that are carrying
# traffic, so the watchdog calls us with just that peer number.
if [[ -n "$ONLY_PEER" && "$ONLY_PEER" =~ ^[0-9]+$ ]]; then
    peer_link_up "$ONLY_PEER"
    [[ -x "$NAT_SCRIPT" ]] && "$NAT_SCRIPT" > /dev/null 2>&1
    exit 0
fi

# ---------- legacy cleanup: v2 used a single un-indexed interface ----------
ip link set midosMOI down 2>/dev/null
ip link del midosMOI 2>/dev/null

# ---------- all peers ----------
for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
    peer_link_up "$i"
done

sysctl -qw "net.ipv4.conf.all.rp_filter=0" 2>/dev/null
[[ -n "$WAN_IF" ]] && sysctl -qw "net.ipv4.conf.${WAN_IF}.rp_filter=0" 2>/dev/null
sysctl -qw net.ipv4.ip_forward=1 2>/dev/null

# ---------- strongswan (mode=ipsec only) ----------
if [[ "$TUNNEL_MODE" == "ipsec" ]]; then
    for s in strongswan-starter strongswan; do
        systemctl list-unit-files 2>/dev/null | grep -q "^${s}\.service" && { systemctl restart "$s" 2>/dev/null; break; }
    done
    sleep 2
    for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
        [[ "$(pget "$i" ENABLED)" == "1" ]] && ipsec up "midos$(pget "$i" IDX)" > /dev/null 2>&1
    done
fi

# ---------- firewall ----------
ipt() { iptables "$@" 2>/dev/null; }

# detach + rebuild our own chains only; never flush other software rules
for c in "$MIDOS_PRE" "$MIDOS_POST"; do
    for _ in 1 2 3 4 5; do ipt -t nat -D PREROUTING  -j "$c" || break; done
    for _ in 1 2 3 4 5; do ipt -t nat -D POSTROUTING -j "$c" || break; done
done
for _ in 1 2 3 4 5; do ipt -D FORWARD -j "$MIDOS_FWD" || break; done
for _ in 1 2 3 4 5; do ipt -D INPUT   -j "$MIDOS_IN"  || break; done
# the jump must be detached BEFORE the chain can be flushed/deleted,
# otherwise -X fails and the next -A appends duplicates to the old chain
for _ in 1 2 3 4 5; do ipt -t mangle -D FORWARD -j MIDOS_MSS || break; done
for c in "$MIDOS_PRE" "$MIDOS_POST" "$MIDOS_FWD" "$MIDOS_IN"; do
    ipt -t nat -F "$c"; ipt -t nat -X "$c"
    ipt -F "$c"; ipt -X "$c"
done
ipt -t mangle -F MIDOS_MSS; ipt -t mangle -X MIDOS_MSS
# client-ip preservation leaves a mangle chain, an ip rule and a route table
for _ in 1 2 3 4 5; do ipt -t mangle -D PREROUTING -j "$MIDOS_MARK" || break; done
ipt -t mangle -F "$MIDOS_MARK"; ipt -t mangle -X "$MIDOS_MARK"
while ip rule list 2>/dev/null | grep -q "fwmark 0x4d"; do ip rule del fwmark 0x4d 2>/dev/null || break; done
ip route flush table 201 2>/dev/null

# --- INPUT: always let the tunnel transport + SSH through ---
ipt -N "$MIDOS_IN"
ipt -A "$MIDOS_IN" -p tcp --dport "${SSH_PORT:-22}" -j ACCEPT
# anti-spoof: a packet claiming to come from one of our tunnel /30s can only
# arrive through a tunnel; rp_filter is off (it has to be), so do it here
[[ -n "$WAN_IF" ]] && ipt -A "$MIDOS_IN" -i "$WAN_IF" -s "${PRIV_BASE:-11.10.10}.0/24" -j DROP
for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
    [[ "$(pget "$i" ENABLED)" == "1" ]] || continue
    if [[ "$ROLE" == "IRAN" ]]; then RP="$(pget "$i" IP)"; else RP="$IRAN_IP"; fi
    ipt -A "$MIDOS_IN" -p gre -s "$RP" -j ACCEPT
    ipt -A "$MIDOS_IN" -p esp -s "$RP" -j ACCEPT
    [[ "$TUNNEL_MODE" == "fou" ]] && \
        ipt -A "$MIDOS_IN" -p udp --dport "$(pget "$i" FOU)" -s "$RP" -j ACCEPT
    [[ "$TUNNEL_MODE" == "espudp" ]] && \
        ipt -A "$MIDOS_IN" -p udp --dport "$(pget "$i" UDPPORT)" -s "$RP" -j ACCEPT
    [[ "$TUNNEL_MODE" == "ipsec" && -n "$IKE_PORT" ]] && \
        ipt -A "$MIDOS_IN" -p udp --dport "$IKE_PORT" -s "$RP" -j ACCEPT
    ipt -A "$MIDOS_IN" -i "${TUNNEL_BASE}+" -s "$(pget "$i" NET)" -j ACCEPT
done
ipt -A "$MIDOS_IN" -i "${TUNNEL_BASE}+" -j ACCEPT
ipt -I INPUT 1 -j "$MIDOS_IN"

# --- FORWARD: allow tunnel traffic both ways (one wildcard covers every peer) ---
ipt -N "$MIDOS_FWD"
[[ -n "$WAN_IF" ]] && ipt -A "$MIDOS_FWD" -i "$WAN_IF" -s "${PRIV_BASE:-11.10.10}.0/24" -j DROP
ipt -A "$MIDOS_FWD" -i "${TUNNEL_BASE}+" -j ACCEPT
ipt -A "$MIDOS_FWD" -o "${TUNNEL_BASE}+" -j ACCEPT
ipt -I FORWARD 1 -j "$MIDOS_FWD"

# --- MSS clamping: without it, big TLS packets silently black-hole
#     because PMTU discovery is broken by most filtering middleboxes ---
if [[ "${TCP_MSS:-0}" -gt 0 ]]; then MSS="$TCP_MSS"; else MSS=$(( TUNNEL_MTU - 40 )); fi
ipt -t mangle -N MIDOS_MSS
ipt -t mangle -A MIDOS_MSS -p tcp --tcp-flags SYN,RST SYN -o "${TUNNEL_BASE}+" -j TCPMSS --set-mss "$MSS"
ipt -t mangle -A MIDOS_MSS -p tcp --tcp-flags SYN,RST SYN -i "${TUNNEL_BASE}+" -j TCPMSS --set-mss "$MSS"
ipt -t mangle -I FORWARD 1 -j MIDOS_MSS

ipt -t nat -N "$MIDOS_POST"
if [[ "$ROLE" == "IRAN" ]]; then
    # ===== IRAN: source-NAT each tunnel to our own end of its /30 =====
    # static SNAT is cheaper than MASQUERADE (no per-packet route lookup)
    for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
        [[ "$(pget "$i" ENABLED)" == "1" ]] || continue
        # keeping the client address means NOT rewriting the source here
        [[ "${KEEP_CLIENT_IP:-0}" == "1" ]] && continue
        ipt -t nat -A "$MIDOS_POST" -d "$(pget "$i" REMOTE)" -o "$(pget "$i" IFACE)" \
            -j SNAT --to-source "$(pget "$i" LOCAL)"
    done
    ipt -t nat -A "$MIDOS_POST" -o "${TUNNEL_BASE}+" -j RETURN
    ipt -t nat -I POSTROUTING 1 -j "$MIDOS_POST"
    # PREROUTING/DNAT depends on which peers are alive, so it lives in its own
    # script that the watchdog can re-run on its own
    [[ -x "$NAT_SCRIPT" ]] && "$NAT_SCRIPT" > /dev/null 2>&1
else
    # ===== KHAREJ: masquerade tunnel traffic out to the internet =====
    for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
        if [[ -n "$WAN_IF" ]]; then
            ipt -t nat -A "$MIDOS_POST" -s "$(pget "$i" NET)" -o "$WAN_IF" -j MASQUERADE
        else
            ipt -t nat -A "$MIDOS_POST" -s "$(pget "$i" NET)" ! -o "${TUNNEL_BASE}+" -j MASQUERADE
        fi
    done
    ipt -t nat -I POSTROUTING 1 -j "$MIDOS_POST"

    # Replies to a preserved client address must go back the way they came.
    # The default route points at this server's own WAN, so without a policy
    # route the reply leaves with a source the upstream never routed and dies.
    # Mark the connection on the way in, restore the mark on the way out, and
    # send marked replies to a table whose default is the tunnel.
    if [[ "${KEEP_CLIENT_IP:-0}" == "1" ]]; then
        rt=201; mk=0x4d
        ipt -t mangle -N "$MIDOS_MARK" 2>/dev/null
        ipt -t mangle -F "$MIDOS_MARK"
        ipt -t mangle -A "$MIDOS_MARK" -i "${TUNNEL_BASE}+" -j CONNMARK --set-mark $mk
        ipt -t mangle -A "$MIDOS_MARK" -m connmark --mark $mk -j CONNMARK --restore-mark
        ipt -t mangle -C PREROUTING -j "$MIDOS_MARK" 2>/dev/null || ipt -t mangle -I PREROUTING 1 -j "$MIDOS_MARK"
        ip rule list 2>/dev/null | grep -q "fwmark $mk" || ip rule add fwmark $mk table $rt pref 11000
        for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
            [[ "$(pget "$i" ENABLED)" == "1" ]] || continue
            ip route replace default via "$(pget "$i" REMOTE)" dev "$(pget "$i" IFACE)" table $rt 2>/dev/null && break
        done
        log "client-ip preservation on: replies routed back through the tunnel"
    fi
fi

command -v netfilter-persistent > /dev/null 2>&1 && netfilter-persistent save > /dev/null 2>&1

log "tunnel up: mode=$TUNNEL_MODE role=$ROLE peers=${PEER_COUNT} balance=${BALANCE_MODE} mtu=$TUNNEL_MTU"
exit 0
UPEOF
    chmod +x "$UP_SCRIPT"
}

# The DNAT chain is the only part of the firewall that depends on which peers
# are ALIVE, so it lives in a script of its own: the watchdog can rebuild it in
# milliseconds when a KHAREJ server dies, without touching the working tunnels.
write_nat_script() {
    cat > "$NAT_SCRIPT" << 'NATEOF'
#!/bin/bash
# Midos Tunnel - (re)build the IRAN PREROUTING/DNAT chain from peer health
CONFIG_FILE="/etc/Midos-tunnel/config"
HEALTH_FILE="/etc/Midos-tunnel/health"
LOG_FILE="/var/log/Midos-tunnel.log"
[[ -f "$CONFIG_FILE" ]] || exit 1
# shellcheck disable=SC1090
source "$CONFIG_FILE"
[[ "$ROLE" == "IRAN" ]] || exit 0

MIDOS_PRE="MIDOS_PRE"
TUNNEL_BASE="${TUNNEL_BASE:-midosMOI}"
log() { echo "$(date '+%F %T') [nat] $*" >> "$LOG_FILE"; }
pget() { local v="PEER_${1}_${2}"; echo "${!v}"; }
ipt() { iptables "$@" 2>/dev/null; }

# a peer counts as usable unless the watchdog explicitly marked it down
peer_alive() {
    [[ -f "$HEALTH_FILE" ]] || return 0
    grep -qx "$1 down" "$HEALTH_FILE" && return 1
    return 0
}

ACTIVE=""
for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
    [[ "$(pget "$i" ENABLED)" == "1" ]] || continue
    peer_alive "$i" || continue
    ACTIVE="${ACTIVE} ${i}"
done
# every peer is down: stay pointed at the enabled ones anyway, so traffic
# starts flowing again the instant one of them answers
if [[ -z "${ACTIVE// /}" ]]; then
    for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
        [[ "$(pget "$i" ENABLED)" == "1" ]] && ACTIVE="${ACTIVE} ${i}"
    done
    [[ -n "${ACTIVE// /}" ]] && log "WARNING: no healthy peer, falling back to all enabled peers"
fi

ipt -t nat -N "$MIDOS_PRE"
ipt -t nat -F "$MIDOS_PRE"

# ---- safety RETURNs first - these must never be redirected ----
ipt -t nat -A "$MIDOS_PRE" -p tcp --dport "${SSH_PORT:-22}" -j RETURN
ipt -t nat -A "$MIDOS_PRE" -p gre -j RETURN
ipt -t nat -A "$MIDOS_PRE" -p esp -j RETURN
for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
    [[ "$TUNNEL_MODE" == "fou" ]] && ipt -t nat -A "$MIDOS_PRE" -p udp --dport "$(pget "$i" FOU)" -j RETURN
    [[ "$TUNNEL_MODE" == "espudp" ]] && ipt -t nat -A "$MIDOS_PRE" -p udp --dport "$(pget "$i" UDPPORT)" -j RETURN
    ipt -t nat -A "$MIDOS_PRE" -s "$(pget "$i" NET)" -j RETURN
    ipt -t nat -A "$MIDOS_PRE" -d "$(pget "$i" NET)" -j RETURN
done
[[ -n "$IKE_PORT" ]] && ipt -t nat -A "$MIDOS_PRE" -p udp --dport "$IKE_PORT" -j RETURN
ipt -t nat -A "$MIDOS_PRE" -i "${TUNNEL_BASE}+" -j RETURN
# only brand-new connections need a NAT decision - an established one already
# has its conntrack entry and must stay on the server it was opened against
ipt -t nat -A "$MIDOS_PRE" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN

case "${BALANCE_MODE:-failover}" in

    ports)
        # ---- every peer owns its own port list ----
        #
        # A list entry is either a plain port ("2053") or a remap
        # ("12053:2053" = this server's 12053 -> that peer's 2053).
        #
        # Remapping is what makes several peers that run the SAME service on
        # the SAME port reachable at once: with one public IP, "IP:2053" can
        # only ever point at one of them, so each peer is given its own
        # external port instead. Plain entries still go through multiport in a
        # single rule; only remaps need one rule each, because iptables cannot
        # rewrite different ports in one multiport rule.
        emit_ports() {   # emit_ports <tcp|udp> <list> <peer-ip>
            local proto="$1" list="$2" dst="$3" item plain="" ext int
            local IFS=','; local items=($list); unset IFS
            for item in "${items[@]}"; do
                [[ -z "$item" ]] && continue
                if [[ "$item" == *:* ]]; then
                    ext="${item%%:*}"; int="${item##*:}"
                    ipt -t nat -A "$MIDOS_PRE" -p "$proto" --dport "$ext" -j DNAT --to-destination "${dst}:${int}"
                else
                    plain="${plain:+$plain,}$item"
                fi
            done
            [[ -n "$plain" ]] && ipt -t nat -A "$MIDOS_PRE" -p "$proto" -m multiport --dports "$plain" -j DNAT --to-destination "$dst"
            return 0
        }
        for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
            [[ "$(pget "$i" ENABLED)" == "1" ]] || continue
            t="$(pget "$i" TCP)"; u="$(pget "$i" UDP)"; r="$(pget "$i" REMOTE)"
            [[ -n "$t" ]] && emit_ports tcp "$t" "$r"
            [[ -n "$u" ]] && emit_ports udp "$u" "$r"
        done
        log "ports mode: per-peer port lists installed"
        ;;

    balance)
        # ---- keep the excluded ports (SSH, panels, ...) on this server ----
        [[ -n "$EXCLUDE_TCP_PORTS" ]] && ipt -t nat -A "$MIDOS_PRE" -p tcp -m multiport --dports "$EXCLUDE_TCP_PORTS" -j RETURN
        [[ -n "$EXCLUDE_UDP_PORTS" ]] && ipt -t nat -A "$MIDOS_PRE" -p udp -m multiport --dports "$EXCLUDE_UDP_PORTS" -j RETURN
        # ---- pinned ports: a server that owns specific ports NO OTHER server
        # has (e.g. a unique x-ui inbound) gets them routed only to itself.
        # These rules come first, so those ports skip the balancer; everything
        # else (the ports every server shares) falls through to the weighted
        # spread below. Empty pin lists => pure balance, unchanged behaviour.
        for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
            [[ "$(pget "$i" ENABLED)" == "1" ]] || continue
            pt="$(pget "$i" TCP)"; pu="$(pget "$i" UDP)"; pr="$(pget "$i" REMOTE)"
            [[ -n "$pt" ]] && ipt -t nat -A "$MIDOS_PRE" -p tcp -m multiport --dports "$pt" -j DNAT --to-destination "$pr"
            [[ -n "$pu" ]] && ipt -t nat -A "$MIDOS_PRE" -p udp -m multiport --dports "$pu" -j DNAT --to-destination "$pr"
        done
        # ---- weighted spread over the healthy peers (the shared ports) ----
        # Rule j fires with probability w_j / (sum of the weights not yet
        # offered), which makes the overall share of peer j exactly w_j/total
        # however many peers there are; the last peer catches the remainder.
        total=0
        for i in $ACTIVE; do w="$(pget "$i" WEIGHT)"; total=$(( total + ${w:-1} )); done
        last=""
        for i in $ACTIVE; do last="$i"; done
        acc=0
        for i in $ACTIVE; do
            r="$(pget "$i" REMOTE)"
            w="$(pget "$i" WEIGHT)"; w="${w:-1}"
            if [[ "$i" == "$last" ]]; then
                ipt -t nat -A "$MIDOS_PRE" -p tcp -j DNAT --to-destination "$r"
                ipt -t nat -A "$MIDOS_PRE" -p udp -j DNAT --to-destination "$r"
            else
                prob=$(awk -v w="$w" -v rem="$(( total - acc ))" 'BEGIN{ if (rem <= 0) rem = 1; printf "%.8f", w / rem }')
                ipt -t nat -A "$MIDOS_PRE" -p tcp -m statistic --mode random --probability "$prob" -j DNAT --to-destination "$r"
                ipt -t nat -A "$MIDOS_PRE" -p udp -m statistic --mode random --probability "$prob" -j DNAT --to-destination "$r"
                acc=$(( acc + w ))
            fi
        done
        log "balance mode: peers[${ACTIVE# }] total-weight=${total}"
        ;;

    *)
        # ---- failover: the first healthy peer takes everything ----
        [[ -n "$EXCLUDE_TCP_PORTS" ]] && ipt -t nat -A "$MIDOS_PRE" -p tcp -m multiport --dports "$EXCLUDE_TCP_PORTS" -j RETURN
        [[ -n "$EXCLUDE_UDP_PORTS" ]] && ipt -t nat -A "$MIDOS_PRE" -p udp -m multiport --dports "$EXCLUDE_UDP_PORTS" -j RETURN
        act=""
        for i in $ACTIVE; do act="$i"; break; done
        if [[ -n "$act" ]]; then
            r="$(pget "$act" REMOTE)"
            ipt -t nat -A "$MIDOS_PRE" -p tcp -j DNAT --to-destination "$r"
            ipt -t nat -A "$MIDOS_PRE" -p udp -j DNAT --to-destination "$r"
            log "failover mode: active peer ${act} ($(pget "$act" NAME) / $(pget "$act" IP))"
        else
            log "failover mode: NO peer available - traffic stays local"
        fi
        ;;
esac

# make sure the chain is actually hooked in (it is not after a plain flush)
ipt -t nat -C PREROUTING -j "$MIDOS_PRE" || ipt -t nat -I PREROUTING 1 -j "$MIDOS_PRE"
exit 0
NATEOF
    chmod +x "$NAT_SCRIPT"
}

write_down_script() {
    cat > "$DOWN_SCRIPT" << 'DOWNEOF'
#!/bin/bash
# Midos Tunnel - tear every peer tunnel down (keeps SSH and foreign rules intact)
CONFIG_FILE="/etc/Midos-tunnel/config"
LOG_FILE="/var/log/Midos-tunnel.log"
[[ -f "$CONFIG_FILE" ]] && source "$CONFIG_FILE"
TUNNEL_BASE="${TUNNEL_BASE:-midosMOI}"
MIDOS_PRE="MIDOS_PRE"; MIDOS_POST="MIDOS_POST"; MIDOS_FWD="MIDOS_FWD"; MIDOS_IN="MIDOS_IN"
MIDOS_MARK="MIDOS_MARK"
pget() { local v="PEER_${1}_${2}"; echo "${!v}"; }
ipt() { iptables "$@" 2>/dev/null; }

for c in "$MIDOS_PRE" "$MIDOS_POST"; do
    for _ in 1 2 3 4 5; do ipt -t nat -D PREROUTING  -j "$c" || break; done
    for _ in 1 2 3 4 5; do ipt -t nat -D POSTROUTING -j "$c" || break; done
    ipt -t nat -F "$c"; ipt -t nat -X "$c"
done
for _ in 1 2 3 4 5; do ipt -D FORWARD -j "$MIDOS_FWD" || break; done
for _ in 1 2 3 4 5; do ipt -D INPUT   -j "$MIDOS_IN"  || break; done
ipt -F "$MIDOS_FWD"; ipt -X "$MIDOS_FWD"
ipt -F "$MIDOS_IN";  ipt -X "$MIDOS_IN"
for _ in 1 2 3 4 5; do ipt -t mangle -D FORWARD -j MIDOS_MSS || break; done
ipt -t mangle -F MIDOS_MSS; ipt -t mangle -X MIDOS_MSS
# client-ip preservation leaves a mangle chain, an ip rule and a route table
for _ in 1 2 3 4 5; do ipt -t mangle -D PREROUTING -j "$MIDOS_MARK" || break; done
ipt -t mangle -F "$MIDOS_MARK"; ipt -t mangle -X "$MIDOS_MARK"
while ip rule list 2>/dev/null | grep -q "fwmark 0x4d"; do ip rule del fwmark 0x4d 2>/dev/null || break; done
ip route flush table 201 2>/dev/null

if [[ "$ROLE" == "IRAN" ]]; then SELF_IP="${SELF_IP:-$IRAN_IP}"; else SELF_IP="${SELF_IP:-$KHAREJ_IP}"; fi

for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
    ifc="$(pget "$i" IFACE)"; fou="$(pget "$i" FOU)"
    if [[ "$ROLE" == "IRAN" ]]; then L="$IRAN_IP"; R="$(pget "$i" IP)"; else L="$SELF_IP"; R="$IRAN_IP"; fi

    [[ "$TUNNEL_MODE" == "ipsec" ]] && ipsec down "midos$(pget "$i" IDX)" > /dev/null 2>&1

    if [[ "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" ]]; then
        ip xfrm state deleteall src "$L" dst "$R" proto esp 2>/dev/null
        ip xfrm state deleteall src "$R" dst "$L" proto esp 2>/dev/null
        ip xfrm policy delete src "$L" dst "$R" proto gre dir out 2>/dev/null
        ip xfrm policy delete src "$R" dst "$L" proto gre dir in  2>/dev/null
        ip xfrm policy delete src "$R" dst "$L" proto gre dir fwd 2>/dev/null
    fi

    ip link set "$ifc" down 2>/dev/null
    ip link del "$ifc" 2>/dev/null
    ip tunnel del "$ifc" 2>/dev/null
    [[ -n "$fou" ]] && ip fou del port "$fou" ipproto 47 2>/dev/null
done

# v2 leftovers
ip link set midosMOI down 2>/dev/null
ip link del midosMOI 2>/dev/null

echo "$(date '+%F %T') [down] all tunnels removed" >> "$LOG_FILE"
exit 0
DOWNEOF
    chmod +x "$DOWN_SCRIPT"
}

# Free edition: nothing to enforce. Deleting a leftover enforcer also frees a
# server that was converted from a licensed install.
write_license_script() {
    rm -f "$LIC_SCRIPT" "${CONFIG_DIR}/license.blocked"
}

write_watchdog_script() {
    cat > "$WD_SCRIPT" << 'WDEOF'
#!/bin/bash
# Midos Tunnel - watchdog: per-peer health, auto-repair and DNAT failover.
# MOI is stateless, so a peer reboot or an ISP path change leaves an
# interface that looks UP but drops every packet. This is the fix - and with
# several KHAREJ servers it is also what moves traffic off a dead one.
CONFIG_FILE="/etc/Midos-tunnel/config"
HEALTH_FILE="/etc/Midos-tunnel/health"
LOG_FILE="/var/log/Midos-tunnel.log"
UP_SCRIPT="/usr/local/sbin/Midos-tunnel-up.sh"
NAT_SCRIPT="/usr/local/sbin/Midos-tunnel-nat.sh"
DOWN_SCRIPT="/usr/local/sbin/Midos-tunnel-down.sh"
[[ -f "$CONFIG_FILE" ]] || exit 1
source "$CONFIG_FILE"

INTERVAL="${WD_INTERVAL:-20}"
MAX_FAILS=3
pget() { local v="PEER_${1}_${2}"; echo "${!v}"; }
log() { echo "$(date '+%F %T') [watchdog] $*" >> "$LOG_FILE"; }

declare -A FAILS
declare -A STATE

write_health() {
    local i tmp="${HEALTH_FILE}.tmp"
    : > "$tmp"
    for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
        echo "$i ${STATE[$i]:-up}" >> "$tmp"
    done
    mv -f "$tmp" "$HEALTH_FILE"
}

for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
    STATE[$i]="up"; FAILS[$i]=0
done
write_health
log "started, peers=${PEER_COUNT:-0} interval=${INTERVAL}s mode=${BALANCE_MODE:-failover}"

while true; do
    sleep "$INTERVAL"
    changed=0

    for ((i = 1; i <= ${PEER_COUNT:-0}; i++)); do
        ifc="$(pget "$i" IFACE)"; rem="$(pget "$i" REMOTE)"; name="$(pget "$i" NAME)"

        if [[ "$(pget "$i" ENABLED)" != "1" ]]; then
            if [[ "${STATE[$i]}" != "down" ]]; then STATE[$i]="down"; changed=1; fi
            continue
        fi

        if ! ip link show "$ifc" > /dev/null 2>&1; then
            log "peer $i ($name): interface $ifc missing -> rebuilding"
            "$UP_SCRIPT" "$i" > /dev/null 2>&1
            FAILS[$i]=0
            continue
        fi

        if ping -c 2 -W 3 -I "$ifc" "$rem" > /dev/null 2>&1; then
            if [[ "${STATE[$i]}" == "down" ]]; then
                log "peer $i ($name) RECOVERED -> back in rotation"
                STATE[$i]="up"; changed=1
            fi
            FAILS[$i]=0
        else
            FAILS[$i]=$(( ${FAILS[$i]:-0} + 1 ))
            log "peer $i ($name) unreachable (${FAILS[$i]}/${MAX_FAILS})"
            if (( ${FAILS[$i]} >= MAX_FAILS )); then
                if [[ "${STATE[$i]}" != "down" ]]; then
                    log "peer $i ($name) marked DOWN -> traffic moves to the other peers"
                    STATE[$i]="down"; changed=1
                fi
                # rebuild just this tunnel; the healthy peers keep forwarding
                "$UP_SCRIPT" "$i" > /dev/null 2>&1
                FAILS[$i]=0
            fi
        fi
    done

    if (( changed )); then
        write_health
        [[ "$ROLE" == "IRAN" && -x "$NAT_SCRIPT" ]] && "$NAT_SCRIPT" > /dev/null 2>&1
    fi
done
WDEOF
    chmod +x "$WD_SCRIPT"
}
# The kernel will happily WRAP outgoing ESP in UDP on its own, but it only
# UNWRAPS inbound ESP-in-UDP for a port some socket has claimed with the
# UDP_ENCAP option. Normally an IKE daemon owns that socket; we run no daemon,
# so this tiny holder claims the port and then sleeps forever. It never reads a
# packet and never touches the data path - decryption and routing stay entirely
# in the kernel, which is why this mode keeps kernel speed.
write_encap_script() {
    cat > "$ENCAP_SCRIPT" << 'PYEOF'
#!/usr/bin/env python3
import socket, sys, signal, os

SOL_UDP, UDP_ENCAP, UDP_ENCAP_ESPINUDP = 17, 100, 2

def hold(port):
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port))
    s.setsockopt(SOL_UDP, UDP_ENCAP, UDP_ENCAP_ESPINUDP)
    return s

ports, held = [], []
for a in sys.argv[1:]:
    try:
        p = int(a)
    except ValueError:
        continue
    if 1 <= p <= 65535 and p not in ports:
        ports.append(p)

if not ports:
    sys.stderr.write("no ports given\n"); sys.exit(1)

for p in ports:
    try:
        held.append(hold(p))
    except OSError as e:
        sys.stderr.write("cannot hold udp/%d: %s\n" % (p, e))
        sys.exit(1)

sys.stderr.write("holding %s with UDP_ENCAP_ESPINUDP\n" % ",".join(map(str, ports)))
signal.signal(signal.SIGTERM, lambda *_: os._exit(0))
signal.pause()
PYEOF
    chmod 755 "$ENCAP_SCRIPT"
}

write_services() {
    local role_label="$1" ENCAP_AFTER="" i ports=""
    if [[ "$TUNNEL_MODE" == "espudp" ]]; then
        for ((i = 1; i <= PEER_COUNT; i++)); do
            local up; up="$(pget "$i" UDPPORT)"
            [[ -n "$up" ]] && ports="${ports:+$ports }$up"
        done
        cat > "$ENCAP_SERVICE_FILE" << EOF
[Unit]
Description=Midos Tunnel ESP-in-UDP encapsulation socket
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/bin/env python3 ${ENCAP_SCRIPT} ${ports}
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
        ENCAP_AFTER="Midos-tunnel-encap.service"
        systemctl daemon-reload > /dev/null 2>&1
        systemctl enable --now Midos-tunnel-encap.service > /dev/null 2>&1
    else
        systemctl disable --now Midos-tunnel-encap.service > /dev/null 2>&1
        rm -f "$ENCAP_SERVICE_FILE"
    fi
    cat > "$SERVICE_FILE" << EOF
[Unit]
Description=Midos Tunnel (${role_label})
After=network-online.target ${ENCAP_AFTER}
Wants=network-online.target ${ENCAP_AFTER}

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=${UP_SCRIPT}
ExecStop=${DOWN_SCRIPT}
ExecReload=${UP_SCRIPT}
TimeoutStartSec=60

[Install]
WantedBy=multi-user.target
EOF

    cat > "$WD_SERVICE_FILE" << EOF
[Unit]
Description=Midos Tunnel Watchdog
After=Midos-tunnel.service
Requires=Midos-tunnel.service

[Service]
Type=simple
ExecStart=${WD_SCRIPT}
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

    # esp mode: slide the key window once a minute, independent of the
    # (optional) watchdog - a peer that stopped rotating would be dropped by
    # the other end two epochs later
    cat > "$REKEY_SERVICE_FILE" << EOF
[Unit]
Description=Midos Tunnel ESP rekey
After=Midos-tunnel.service

[Service]
Type=oneshot
ExecStart=${UP_SCRIPT} rekey
EOF
    cat > "$REKEY_TIMER_FILE" << EOF
[Unit]
Description=Midos Tunnel ESP rekey timer

[Timer]
OnBootSec=90
OnUnitActiveSec=60
AccuracySec=5

[Install]
WantedBy=timers.target
EOF
    systemctl daemon-reload
}

# ──────────────────── ANIMATED PING ────────────────────

animated_ping() {
    local target="$1" label="$2" iface="$3"
    local count=4 success=0 fail=0 i res rtt
    local times=()
    local pargs=(-c 1 -W 3)
    [[ -n "$iface" ]] && pargs+=(-I "$iface")

    echo -e "    ${G6}╭───────────────────────────────────────────────────────────╮${NC}"
    echo -e "    ${G6}│${NC}  ${G2}🏓${NC} ${WHITE}${BOLD}${label}${NC}"
    echo -e "    ${G6}│${NC}  ${DIM}Target: ${CYAN}${target}${NC}"
    echo -e "    ${G6}├───────────────────────────────────────────────────────────┤${NC}"

    for ((i = 1; i <= count; i++)); do
        echo -ne "    ${G6}│${NC}  ${DIM}Packet ${i}/${count}...${NC}"
        if res=$(ping "${pargs[@]}" "$target" 2>&1); then
            rtt=$(echo "$res" | grep -oP 'time=\K[0-9.]+' | head -1)
            success=$((success + 1))
            if [[ -n "$rtt" ]]; then
                times+=("$rtt")
                local ri=${rtt%.*}; ri=${ri:-0}
                local clr="${GREEN}"
                ((ri > 100)) && clr="${YELLOW}"
                ((ri > 300)) && clr="${RED}"
                echo -e "\r    ${G6}│${NC}  ${GREEN}✔${NC} Pkt ${i}: ${clr}${rtt} ms${NC}                                 "
            else
                echo -e "\r    ${G6}│${NC}  ${GREEN}✔${NC} Pkt ${i}: ${GREEN}OK${NC}                                       "
            fi
        else
            fail=$((fail + 1))
            echo -e "\r    ${G6}│${NC}  ${RED}✘${NC} Pkt ${i}: ${RED}Timeout${NC}                                   "
        fi
        sleep 0.2
    done

    echo -e "    ${G6}├───────────────────────────────────────────────────────────┤${NC}"
    local loss=$((fail * 100 / count))

    if ((success > 0)); then
        local min_t="" max_t="" sum_t="0" t
        for t in "${times[@]}"; do
            sum_t=$(awk "BEGIN{printf \"%.2f\",$sum_t+$t}")
            [[ -z "$min_t" ]] && min_t="$t"
            [[ -z "$max_t" ]] && max_t="$t"
            awk "BEGIN{exit !($t < $min_t)}" && min_t="$t"
            awk "BEGIN{exit !($t > $max_t)}" && max_t="$t"
        done
        local lc="${GREEN}"
        ((loss > 0)) && lc="${YELLOW}"
        echo -e "    ${G6}│${NC}  ${GREEN}✅ CONNECTED${NC}  ${success}/${count}  ${lc}(${loss}% loss)${NC}"
        if ((${#times[@]} > 0)); then
            local avg_t jitter
            avg_t=$(awk "BEGIN{printf \"%.2f\",$sum_t/${#times[@]}}")
            jitter=$(awk "BEGIN{printf \"%.2f\",$max_t-$min_t}")
            echo -e "    ${G6}│${NC}  ${G2}⏱${NC}  min=${CYAN}${min_t}${NC} avg=${CYAN}${avg_t}${NC} max=${CYAN}${max_t}${NC} ms  jitter=${CYAN}${jitter}${NC} ms"
            local ai=${avg_t%.*}; ai=${ai:-0}
            local q="" qb=""
            if ((ai <= 30));    then q="${GREEN}${BOLD}EXCELLENT${NC}"; qb="${GREEN}██████████${NC}"
            elif ((ai <= 80));  then q="${GREEN}VERY GOOD${NC}";        qb="${GREEN}████████${NC}${DIM}██${NC}"
            elif ((ai <= 150)); then q="${YELLOW}GOOD${NC}";            qb="${YELLOW}██████${NC}${DIM}████${NC}"
            elif ((ai <= 300)); then q="${YELLOW}FAIR${NC}";            qb="${YELLOW}████${NC}${DIM}██████${NC}"
            else                     q="${RED}POOR${NC}";               qb="${RED}██${NC}${DIM}████████${NC}"
            fi
            echo -e "    ${G6}│${NC}  ${G2}📶${NC}  [${qb}] ${q}"
        fi
    else
        echo -e "    ${G6}│${NC}  ${RED}❌ DISCONNECTED${NC}  ${RED}100% loss${NC}"
    fi
    echo -e "    ${G6}╰───────────────────────────────────────────────────────────╯${NC}"
    echo ""
}

# ──────────────────── SETUP ────────────────────

# rewrite every generated file from the current in-memory config
regenerate_all() {
    save_config
    write_up_script
    write_nat_script
    write_down_script
    write_watchdog_script
    write_license_script
    write_encap_script
    write_services "$ROLE"
    [[ "$TUNNEL_MODE" == "ipsec" ]] && write_ipsec_config
    return 0
}

apply_now() {
    systemctl restart Midos-tunnel.service > /dev/null 2>&1
    if [[ ( "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" ) && "${ESP_ROTATE_SEC:-$DEF_ESP_ROTATE_SEC}" -gt 0 ]]; then
        systemctl enable --now Midos-tunnel-rekey.timer > /dev/null 2>&1
    else
        systemctl disable --now Midos-tunnel-rekey.timer > /dev/null 2>&1
    fi
    if [[ "$WATCHDOG" == "1" ]]; then
        systemctl enable Midos-tunnel-watchdog.service > /dev/null 2>&1
        systemctl restart Midos-tunnel-watchdog.service > /dev/null 2>&1
    else
        systemctl disable --now Midos-tunnel-watchdog.service > /dev/null 2>&1
    fi
}

# what the user has to type on each KHAREJ box - the only manual step left
print_kharej_instructions() {
    local i
    echo ""
    echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
    echo -e "${G3}    │${NC}  ${G1}📋${NC} ${WHITE}${BOLD}NOW RUN THIS SCRIPT ON EACH KHAREJ SERVER (option 2)${NC}       ${G3}│${NC}"
    echo -e "${G3}    ├──────────────────────────────────────────────────────────────┤${NC}"
    for ((i = 1; i <= PEER_COUNT; i++)); do
        printf "    ${G3}│${NC}  ${G2}%s${NC} ${CYAN}%-15s${NC} ${DIM}→${NC} slot ${WHITE}%-3s${NC} ${DIM}(%s ↔ %s)${NC}%*s${G3}│${NC}\n" \
            "$(pget "$i" NAME)" "$(pget "$i" IP)" "$(pget "$i" IDX)" \
            "$(pget "$i" LOCAL)" "$(pget "$i" REMOTE)" 2 ""
    done
    echo -e "${G3}    ├──────────────────────────────────────────────────────────────┤${NC}"
    printf "    ${G3}│${NC}  ${DIM}mode=%-8s mtu=%-6s IRAN IP=%-15s${NC}      ${G3}│${NC}\n" "$TUNNEL_MODE" "$TUNNEL_MTU" "$IRAN_IP"
    [[ "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" || "$TUNNEL_MODE" == "ipsec" ]] && \
    echo -e "${G3}    │${NC}  ${DIM}+ the SAME passphrase on every server${NC}                       ${G3}│${NC}"
    echo -e "${G3}    │${NC}  ${DIM}MOI key / FOU port are derived per IP pair - no typing${NC}      ${G3}│${NC}"
    [[ "$TUNNEL_MODE" != "ipsec" ]] && \
    echo -e "${G3}    │${NC}  ${DIM}Slot: just press Enter on KHAREJ - it is detected${NC}           ${G3}│${NC}"
    [[ "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" ]] && \
    echo -e "${G3}    │${NC}  ${DIM}Every server must run Midos Tunnel 3.1+ (rotating ESP keys)${NC} ${G3}│${NC}"
    echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
}

# KHAREJ: find the slot IRAN gave us by trying each one until IRAN answers
# through the tunnel. Only the right slot can answer: IRAN picks the tunnel by
# our public IP, so on a wrong slot our inner address belongs to another
# KHAREJ's /30 and IRAN routes the reply there - and in esp mode the SPIs are
# wrong too, so IRAN cannot even decrypt the probe.
autodetect_slot() {
    local s prev="" ifc rem
    systemctl stop Midos-tunnel-watchdog.service > /dev/null 2>&1
    info "Detecting this server's slot on IRAN ${CYAN}${IRAN_IP}${NC} ..."
    for ((s = 1; s <= MAX_PEERS; s++)); do
        printf "\r    ${G2}❯${NC} Trying slot ${CYAN}%-3s${NC} " "$s"
        # the previous attempt's interface has another name - drop it first,
        # two MOI links with the same endpoints and key cannot coexist
        [[ -n "$prev" ]] && ip link del "${TUNNEL_BASE}${prev}" 2>/dev/null
        clear_peers
        PEER_INDEX="$s"
        add_peer "$IRAN_IP" "iran" "$s"
        save_config
        if [[ -z "$prev" ]]; then
            "$UP_SCRIPT" > /dev/null 2>&1        # first round builds the firewall too
        else
            "$UP_SCRIPT" 1 > /dev/null 2>&1      # afterwards only the tunnel changes
        fi
        prev="$s"
        ifc="$(pget 1 IFACE)"; rem="$(pget 1 REMOTE)"
        if ping -c 3 -i 0.3 -W 1 -I "$ifc" "$rem" > /dev/null 2>&1; then
            echo ""
            ok "IRAN answered on slot ${CYAN}${s}${NC} → me=${CYAN}$(slot_kharej_ip "$s")${NC} iran=${CYAN}$(slot_iran_ip "$s")${NC}"
            return 0
        fi
    done
    echo ""
    ip link del "${TUNNEL_BASE}${prev}" 2>/dev/null
    return 1
}

do_setup() {
    local role="$1" icon i tip
    [[ "$role" == "IRAN" ]] && icon="🇮🇷" || icon="🌍"

    show_logo
    echo -e "    ${BG_GOLD}${WHITE}${BOLD}  ⚡ ${role} Server ${icon}  ${NC}"

    TUNNEL_MTU="$DEF_TUNNEL_MTU"
    TCP_MSS="${TCP_MSS:-$DEF_TCP_MSS}"
    BALANCE_MODE="$DEF_BALANCE_MODE"
    EXCLUDE_TCP_PORTS=""
    EXCLUDE_UDP_PORTS=""
    IPSEC_PSK=""
    SHAPE_MBIT="${SHAPE_MBIT:-$DEF_SHAPE_MBIT}"
    WATCHDOG="${WATCHDOG:-$DEF_WATCHDOG}"
    ESP_ROTATE_SEC="${ESP_ROTATE_SEC:-$DEF_ESP_ROTATE_SEC}"
    ESP_TFC_PAD="${ESP_TFC_PAD:-$DEF_ESP_TFC_PAD}"
    INSTALL_DATE=""

    if [[ "$role" == "IRAN" ]]; then
        input_iran_config || return
    else
        input_kharej_config || return
    fi

    echo ""
    stp "1/8" "Loading kernel modules..."
    ensure_modules
    lsmod | grep -q "^ip_gre" && ok "ip_gre loaded" || warn "ip_gre not visible (built-in?)"
    # rotating ESP keys need both clocks within a couple of hours of each other
    timedatectl set-ntp true > /dev/null 2>&1 || true
    if [[ "$TUNNEL_MODE" == "fou" ]]; then
        lsmod | grep -q "^fou" && ok "fou loaded" || warn "fou not visible"
    fi

    stp "2/8" "Installing dependencies..."
    install_deps
    ok "Packages ready"

    stp "3/8" "Kernel tuning (BBR + buffers + rp_filter fix)..."
    write_sysctl
    local cc
    cc=$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)
    [[ "$cc" == "bbr" ]] && ok "Congestion control: ${GREEN}BBR${NC}" || warn "BBR unavailable, using ${cc}"
    ok "rp_filter disabled, forwarding ON, buffers enlarged"

    stp "4/8" "Writing configuration for ${PEER_COUNT} peer(s)..."
    save_config
    rm -f "$HEALTH_FILE"
    ok "Saved to ${CYAN}${CONFIG_FILE}${NC}"

    if [[ "$TUNNEL_MODE" == "ipsec" ]]; then
        stp "5/8" "Configuring IPsec (strongswan), one conn per peer..."
        write_ipsec_config
        local svc
        svc=$(ipsec_service_name)
        if [[ -n "$svc" ]]; then
            systemctl enable "$svc" > /dev/null 2>&1
            ok "strongswan configured (NAT-T port ${CYAN}${IKE_PORT}${NC})"
        else
            err "strongswan service not found - install failed?"
        fi
    else
        stp "5/8" "Transport: $(mode_label "$TUNNEL_MODE")"
        [[ "$TUNNEL_MODE" == "fou" ]] && ok "Each peer gets its own UDP port"
        [[ "$TUNNEL_MODE" == "gre" ]] && warn "Plain MOI is easily fingerprinted by DPI."
    fi

    stp "6/8" "Generating tunnel scripts..."
    write_up_script
    write_nat_script
    write_down_script
    write_watchdog_script
    write_license_script
    write_encap_script
    ok "up / nat / down / watchdog created"

    stp "7/8" "Installing services..."
    write_services "$role"
    systemctl enable Midos-tunnel.service > /dev/null 2>&1
    [[ "$WATCHDOG" == "1" ]] && systemctl enable Midos-tunnel-watchdog.service > /dev/null 2>&1
    ok "Enabled at boot"

    stp "8/8" "Starting ${PEER_COUNT} tunnel(s)..."
    if [[ "$role" == "KHAREJ" && "${SLOT_AUTO:-0}" == "1" ]]; then
        if ! autodetect_slot; then
            err "IRAN did not answer on any slot."
            info "Check: IRAN is set up and lists ${CYAN}${SELF_IP}${NC}, same mode and passphrase."
            local ms=""
            while ! [[ "$ms" =~ ^[0-9]+$ ]] || ((ms < 1 || ms > MAX_PEERS)); do
                echo -ne "    ${G2}❯${NC} Enter the slot manually (IRAN server list, 1..${MAX_PEERS}): "
                read -r ms
            done
            clear_peers; PEER_INDEX="$ms"
            add_peer "$IRAN_IP" "iran" "$ms"
            save_config
        fi
    fi
    apply_now
    sleep 2

    for ((i = 1; i <= PEER_COUNT; i++)); do
        if ip link show "$(pget "$i" IFACE)" 2>/dev/null | grep -qE "UP|UNKNOWN"; then
            tip=$(ip -4 addr show "$(pget "$i" IFACE)" 2>/dev/null | grep -oP 'inet \K[0-9.]+')
            ok "${GREEN}${BOLD}$(pget "$i" IFACE) UP${NC} ${DIM}($(pget "$i" NAME))${NC} IP: ${CYAN}${tip}${NC}"
        else
            err "$(pget "$i" IFACE) did not come up - check ${CYAN}${LOG_FILE}${NC}"
        fi
    done

    echo ""
    progress_bar 1 "Finalizing"
    echo ""

    animated_ping "$(pget 1 LOCAL)" "Local Self-Ping ($(pget 1 LOCAL))"
    for ((i = 1; i <= PEER_COUNT; i++)); do
        animated_ping "$(pget "$i" REMOTE)" "Peer $(pget "$i" NAME) ($(pget "$i" IP))" "$(pget "$i" IFACE)"
    done

    if [[ "$role" == "IRAN" ]]; then
        print_kharej_instructions
    else
        echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
        echo -e "${G3}    │${NC} ${GREEN}${BOLD}✅ KHAREJ READY${NC}                                              ${G3}│${NC}"
        printf "    ${G3}│${NC} ${DIM}slot=%-3s iface=%-10s mode=%-8s mtu=%-6s${NC}       ${G3}│${NC}\n" \
            "$PEER_INDEX" "$(pget 1 IFACE)" "$TUNNEL_MODE" "$TUNNEL_MTU"
        echo -e "${G3}    │${NC} ${DIM}The IRAN server must list this server on the same slot.${NC}     ${G3}│${NC}"
        echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
    fi
    echo ""
    echo -ne "    ${G2}❯${NC} Press Enter..."
    read -r
}

setup_iran()   { do_setup "IRAN"; }
setup_kharej() { do_setup "KHAREJ"; }

# ──────────────────── NON-INTERACTIVE PROVISIONING ────────────────────
# provision brings up a kernel tunnel with no menu and no prompts, taking every
# input from the environment so the web panel (and any automation) can create a
# tunnel the same way the menu does. It reuses the very same worker functions as
# do_setup, so the result is identical - only the input and the animations are
# gone. The interactive flow above is untouched.
#
#   MIDOS_ROLE      IRAN | KHAREJ                (required)
#   MIDOS_SELF_IP   this server's public IP      (required)
#   MIDOS_MODE      esp | espudp | gre | fou | ipsec   (default esp)
#   MIDOS_PSK       shared passphrase            (required for esp/espudp/ipsec)
#   MIDOS_MTU, MIDOS_SHAPE, MIDOS_WATCHDOG, MIDOS_ESP_ROTATE   (optional)
# IRAN only:
#   MIDOS_PEERS     "ip[:label[:slot]][,ip:...]" (required; one entry per KHAREJ)
#   MIDOS_TCP_PORTS, MIDOS_UDP_PORTS             (optional, forwarded to peers)
#   MIDOS_PEER_<slot>_TCP, MIDOS_PEER_<slot>_UDP (optional, that peer's own
#                   ports instead of the shared ones; "12053:2053" = this
#                   server's 12053 to the peer's 2053)
#   MIDOS_BALANCE   balance mode                 (optional)
# KHAREJ only:
#   MIDOS_IRAN_IP   the IRAN server's public IP  (required)
#   MIDOS_SLOT      slot number, or "auto"       (default auto; ipsec needs a number)
provision() {
    check_root
    local role="${MIDOS_ROLE^^}"
    [[ "$role" == "IRAN" || "$role" == "KHAREJ" ]] || { echo "provision: MIDOS_ROLE must be IRAN or KHAREJ" >&2; return 2; }

    TUNNEL_MTU="${MIDOS_MTU:-$DEF_TUNNEL_MTU}"
    TCP_MSS="${MIDOS_MSS:-$DEF_TCP_MSS}"
    BALANCE_MODE="${MIDOS_BALANCE:-$DEF_BALANCE_MODE}"
    EXCLUDE_TCP_PORTS=""
    EXCLUDE_UDP_PORTS=""
    SHAPE_MBIT="${MIDOS_SHAPE:-$DEF_SHAPE_MBIT}"
    WATCHDOG="${MIDOS_WATCHDOG:-$DEF_WATCHDOG}"
    ESP_ROTATE_SEC="${MIDOS_ESP_ROTATE:-$DEF_ESP_ROTATE_SEC}"
    ESP_TFC_PAD="${MIDOS_ESP_TFC:-$DEF_ESP_TFC_PAD}"
    INSTALL_DATE=""
    PRIV_BASE="$DEF_PRIV_BASE"
    TUNNEL_MODE="${MIDOS_MODE:-esp}"
    IPSEC_PSK="${MIDOS_PSK:-}"
    FORWARD_TCP_PORTS="${MIDOS_TCP_PORTS:-}"
    FORWARD_UDP_PORTS="${MIDOS_UDP_PORTS:-}"
    clear_peers

    case "$TUNNEL_MODE" in
        esp | espudp | gre | fou | ipsec) ;;
        *) echo "provision: MIDOS_MODE must be esp, espudp, gre, fou or ipsec" >&2; return 2 ;;
    esac
    case "$TUNNEL_MODE" in
        esp | espudp | ipsec)
            [[ ${#IPSEC_PSK} -ge 12 ]] || { echo "provision: MIDOS_PSK must be at least 12 chars for $TUNNEL_MODE" >&2; return 2; } ;;
    esac

    if [[ "$role" == "IRAN" ]]; then
        ROLE="IRAN"
        IRAN_IP="$MIDOS_SELF_IP"
        validate_ip "$IRAN_IP" || { echo "provision: bad MIDOS_SELF_IP '$IRAN_IP'" >&2; return 2; }
        SELF_IP="$IRAN_IP"
        KHAREJ_IP=""
        local specs spec ip label slot
        IFS=',' read -ra specs <<< "${MIDOS_PEERS:-$MIDOS_KHAREJ_IP}"
        for spec in "${specs[@]}"; do
            spec="$(echo "$spec" | xargs)"
            [[ -z "$spec" ]] && continue
            IFS=':' read -r ip label slot <<< "$spec"
            validate_ip "$ip" || { echo "provision: bad peer IP '$ip'" >&2; return 2; }
            [[ "$ip" == "$IRAN_IP" ]] && { echo "provision: peer '$ip' is the IRAN server itself" >&2; return 2; }
            peer_ip_used "$ip" && { echo "provision: peer '$ip' listed twice" >&2; return 2; }
            label="${label:-kharej$((PEER_COUNT + 1))}"
            label=$(echo "$label" | tr -cd '[:alnum:]_-' | cut -c1-14)
            [[ -z "$slot" ]] && slot=$(next_free_index)
            [[ "$slot" =~ ^[0-9]+$ ]] && ((slot >= 1 && slot <= MAX_PEERS)) || { echo "provision: bad slot '$slot' for '$ip'" >&2; return 2; }
            # a peer may carry its own ports (MIDOS_PEER_<slot>_TCP/_UDP, set
            # even when empty); otherwise it takes the shared lists
            local vt="MIDOS_PEER_${slot}_TCP" vu="MIDOS_PEER_${slot}_UDP" pt="$FORWARD_TCP_PORTS" pu="$FORWARD_UDP_PORTS"
            [[ -n "${!vt+x}" ]] && pt="${!vt}"
            [[ -n "${!vu+x}" ]] && pu="${!vu}"
            validate_ports "$pt" && validate_ports "$pu" || { echo "provision: bad ports for '$ip'" >&2; return 2; }
            add_peer "$ip" "$label" "$slot" 1 "$pt" "$pu"
            KHAREJ_IP="$ip"
        done
        ((PEER_COUNT > 0)) || { echo "provision: IRAN needs at least one peer in MIDOS_PEERS" >&2; return 2; }
        IKE_PORT=$(derive_num "midos-ike|${IRAN_IP}" 20000 60000)
    else
        ROLE="KHAREJ"
        KHAREJ_IP="$MIDOS_SELF_IP"
        validate_ip "$KHAREJ_IP" || { echo "provision: bad MIDOS_SELF_IP '$KHAREJ_IP'" >&2; return 2; }
        SELF_IP="$KHAREJ_IP"
        IRAN_IP="$MIDOS_IRAN_IP"
        validate_ip "$IRAN_IP" || { echo "provision: bad MIDOS_IRAN_IP '$IRAN_IP'" >&2; return 2; }
        SLOT_AUTO=0
        local slot="${MIDOS_SLOT:-auto}"
        if [[ -z "$slot" || "$slot" == "auto" ]]; then
            [[ "$TUNNEL_MODE" == "ipsec" ]] && { echo "provision: ipsec needs an explicit MIDOS_SLOT" >&2; return 2; }
            SLOT_AUTO=1
            PEER_INDEX=1
        else
            PEER_INDEX="$slot"
        fi
        { [[ "$PEER_INDEX" =~ ^[0-9]+$ ]] && ((PEER_INDEX >= 1 && PEER_INDEX <= MAX_PEERS)); } || { echo "provision: MIDOS_SLOT must be 1..$MAX_PEERS" >&2; return 2; }
        add_peer "$IRAN_IP" "iran" "$PEER_INDEX"
        IKE_PORT=$(derive_num "midos-ike|${IRAN_IP}" 20000 60000)
        BALANCE_MODE="$DEF_BALANCE_MODE"
        SSH_PORT=$(detect_ssh_port)
        EXCLUDE_TCP_PORTS="$SSH_PORT"
    fi

    # ---- apply: the same steps do_setup runs, with no prompts or animation ----
    ensure_modules
    timedatectl set-ntp true > /dev/null 2>&1 || true
    install_deps
    write_sysctl
    save_config
    rm -f "$HEALTH_FILE"
    if [[ "$TUNNEL_MODE" == "ipsec" ]]; then
        write_ipsec_config
        local svc
        svc=$(ipsec_service_name)
        [[ -n "$svc" ]] && systemctl enable "$svc" > /dev/null 2>&1
    fi
    write_up_script
    write_nat_script
    write_down_script
    write_watchdog_script
    write_license_script
    write_encap_script
    write_services "$role"
    systemctl enable Midos-tunnel.service > /dev/null 2>&1
    [[ "$WATCHDOG" == "1" ]] && systemctl enable Midos-tunnel-watchdog.service > /dev/null 2>&1
    if [[ "$role" == "KHAREJ" && "${SLOT_AUTO:-0}" == "1" ]]; then
        if ! autodetect_slot; then
            echo "provision: could not auto-detect the slot; set MIDOS_SLOT explicitly (IRAN must already list this server)" >&2
            return 2
        fi
    fi
    apply_now
    echo "provision: ${role} ready - mode=${TUNNEL_MODE}, peers=${PEER_COUNT}, slot=${PEER_INDEX:-n/a}"
    return 0
}

# teardown removes the kernel tunnel without touching the relays or the web
# panel. It is the non-interactive counterpart of the menu's remove action.
teardown() {
    check_root
    systemctl stop Midos-tunnel-watchdog.service Midos-tunnel.service 2> /dev/null
    systemctl disable Midos-tunnel-watchdog.service Midos-tunnel.service 2> /dev/null
    systemctl disable --now Midos-tunnel-rekey.timer 2> /dev/null
    systemctl stop Midos-tunnel-rekey.service Midos-tunnel-encap.service 2> /dev/null
    systemctl disable Midos-tunnel-encap.service 2> /dev/null
    [[ -x "$DOWN_SCRIPT" ]] && bash "$DOWN_SCRIPT" > /dev/null 2>&1
    rm -f "$CONFIG_FILE" "$HEALTH_FILE"
    rm -f "$SERVICE_FILE" "$REKEY_SERVICE_FILE" "$REKEY_TIMER_FILE" "$ENCAP_SERVICE_FILE" \
        /etc/systemd/system/Midos-tunnel-watchdog.service
    systemctl daemon-reload 2> /dev/null
    echo "teardown: kernel tunnel removed (relays and web panel untouched)"
    return 0
}

# ──────────────────── SERVER MANAGER (IRAN) ────────────────────

list_peers() {
    local i
    echo ""
    echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
    printf "    ${G3}│${NC}  ${G1}🌐${NC} ${WHITE}${BOLD}KHAREJ SERVERS${NC}   ${DIM}%-38s${NC}${G3}│${NC}\n" "$(balance_label "$BALANCE_MODE")"
    echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
    echo ""
    if ((PEER_COUNT == 0)); then
        warn "No servers configured."
        return
    fi
    for ((i = 1; i <= PEER_COUNT; i++)); do
        peer_summary_line "$i"
        case "$BALANCE_MODE" in
            ports)   printf "        ${DIM}tcp${NC} ${CYAN}%s${NC}  ${DIM}udp${NC} ${CYAN}%s${NC}\n" "$(pget "$i" TCP)" "$(pget "$i" UDP)" ;;
            balance) printf "        ${DIM}weight${NC} ${CYAN}%s${NC}\n" "$(pget "$i" WEIGHT)" ;;
            *)       ((i == 1)) && printf "        ${DIM}priority 1 - carries all traffic while healthy${NC}\n" ;;
        esac
    done
}

# prints ONLY the chosen number on stdout - prompts go to stderr so the
# caller can do  n=$(pick_peer)  without swallowing the UI text
pick_peer() {
    local input
    echo -ne "    ${G2}❯${NC} Server number [${CYAN}1-${PEER_COUNT}${NC}] (0 = cancel): " >&2
    read -r input
    [[ "$input" =~ ^[0-9]+$ ]] || { err "Invalid." >&2; return 1; }
    ((input < 1 || input > PEER_COUNT)) && return 1
    echo "$input"
    return 0
}

peer_add_interactive() {
    local ip name idx
    ((PEER_COUNT >= MAX_PEERS)) && { err "Limit is ${MAX_PEERS} servers."; return 1; }
    echo ""
    echo -ne "    ${G2}❯${NC} New KHAREJ Public IP: "
    read -r ip
    validate_ip "$ip" || { err "Invalid IP."; return 1; }
    [[ "$ip" == "$IRAN_IP" ]] && { err "That is this server."; return 1; }
    peer_ip_used "$ip" && { err "Already configured."; return 1; }
    echo -ne "    ${G2}❯${NC} Label [${CYAN}kharej$((PEER_COUNT + 1))${NC}]: "
    read -r name
    name="${name:-kharej$((PEER_COUNT + 1))}"
    name=$(echo "$name" | tr -cd '[:alnum:]_-' | cut -c1-14)
    idx=$(next_free_index)
    add_peer "$ip" "$name" "$idx"
    KHAREJ_IP="$ip"
    case "$BALANCE_MODE" in
        ports)   ask_peer_ports  "$PEER_COUNT" ;;
        balance) ask_peer_weight "$PEER_COUNT" ;;
    esac
    ok "Added ${WHITE}${name}${NC} on slot ${CYAN}${idx}${NC} → iface ${CYAN}${TUNNEL_BASE}${idx}${NC}"
    info "On ${CYAN}${ip}${NC} run this script, option ${WHITE}2${NC}, and enter slot ${WHITE}${idx}${NC}."
    return 0
}

peer_remove_interactive() {
    local n ifc
    n=$(pick_peer) || { info "Cancelled."; return 1; }
    ifc="$(pget "$n" IFACE)"
    echo -ne "    ${RED}❯${NC} Remove ${WHITE}$(pget "$n" NAME)${NC} (${CYAN}$(pget "$n" IP)${NC})? [y/${GREEN}N${NC}]: "
    local c; read -r c
    [[ "$c" != "y" && "$c" != "Y" ]] && { info "Cancelled."; return 1; }
    ip link set "$ifc" down 2>/dev/null
    ip link del "$ifc" 2>/dev/null
    remove_peer "$n"
    ((PEER_COUNT == 0)) && KHAREJ_IP=""
    ok "Removed. ${DIM}Its slot is now free for a new server.${NC}"
    return 0
}

peer_toggle_interactive() {
    local n
    n=$(pick_peer) || { info "Cancelled."; return 1; }
    if [[ "$(pget "$n" ENABLED)" == "1" ]]; then
        pset "$n" ENABLED 0
        ok "$(pget "$n" NAME) disabled ${DIM}(config kept, no traffic, no tunnel)${NC}"
    else
        pset "$n" ENABLED 1
        ok "$(pget "$n" NAME) enabled"
    fi
    return 0
}

peer_priority_interactive() {
    local n f tmp
    info "Priority = order in the list. #1 is the active peer in failover mode."
    n=$(pick_peer) || { info "Cancelled."; return 1; }
    ((n == 1)) && { info "Already first."; return 1; }
    for f in IP NAME IDX IFACE LOCAL REMOTE NET KEY FOU UDPPORT WEIGHT TCP UDP ENABLED; do
        tmp="$(pget "$n" "$f")"
        pset "$n" "$f" "$(pget "$((n - 1))" "$f")"
        pset "$((n - 1))" "$f" "$tmp"
    done
    ok "Moved up."
    return 0
}

peer_edit_interactive() {
    local n
    n=$(pick_peer) || { info "Cancelled."; return 1; }
    case "$BALANCE_MODE" in
        ports)   ask_peer_ports  "$n" ;;
        balance) ask_peer_weight "$n" ;;
        *)       info "Failover mode has nothing per-server to edit; use priority." ; return 1 ;;
    esac
    return 0
}

manage_servers() {
    local choice dirty=0
    if ! load_config; then
        show_logo; err "Nothing installed yet. Run setup first."
        echo ""; echo -ne "    Press Enter..."; read -r; return
    fi
    if [[ "$ROLE" != "IRAN" ]]; then
        show_logo
        err "The server list lives on the IRAN server."
        info "This box is a KHAREJ endpoint (slot ${CYAN}${PEER_INDEX}${NC}); it only ever"
        info "talks to ${CYAN}${IRAN_IP}${NC}. Add/remove servers from the IRAN side."
        echo ""; echo -ne "    Press Enter..."; read -r; return
    fi

    while true; do
        show_logo
        list_peers
        echo ""
        echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
        echo -e "${G3}    │${NC}   ${G2}[1]${NC} ➤ Add KHAREJ server                                   ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[2]${NC} ➤ Remove server                                       ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[3]${NC} ➤ Enable / disable server                             ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[4]${NC} ➤ Edit ports / weight                                 ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[5]${NC} ➤ Move server up (priority)                           ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[6]${NC} ➤ Change distribution mode                            ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[7]${NC} ➤ Show KHAREJ install instructions                    ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[8]${NC} ➤ Apply now ${DIM}(rebuild tunnels + NAT)${NC}                    ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${RED}[0]${NC} ➤ Back                                                ${G3}│${NC}"
        echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
        echo ""
        echo -ne "    ${G2}❯${NC} Choice: "
        read -r choice
        case "$choice" in
            1) peer_add_interactive      && dirty=1 ;;
            2) peer_remove_interactive   && dirty=1 ;;
            3) peer_toggle_interactive   && dirty=1 ;;
            4) peer_edit_interactive     && dirty=1 ;;
            5) peer_priority_interactive && dirty=1 ;;
            6) choose_balance_mode; dirty=1 ;;
            7) print_kharej_instructions ;;
            8)
                regenerate_all
                apply_now
                sleep 1
                ok "Applied - ${PEER_COUNT} peer(s), $(balance_label "$BALANCE_MODE")"
                dirty=0
                ;;
            0)
                if ((dirty)); then
                    echo -ne "    ${G2}❯${NC} Apply the changes now? [${GREEN}Y${NC}/n]: "
                    read -r choice
                    if [[ "$choice" != "n" && "$choice" != "N" ]]; then
                        regenerate_all; apply_now; sleep 1; ok "Applied"
                    else
                        regenerate_all
                        warn "Saved but not applied - run menu 4 (Restart) later."
                    fi
                fi
                return
                ;;
            *) err "Invalid!" ;;
        esac
        if ((dirty)) && [[ "$BALANCE_MODE" == "ports" ]]; then
            local clash; clash="$(ports_collision)"
            if [[ -n "$clash" ]]; then
                echo ""
                err "Port clash - only the FIRST of these servers would get the port:"
                echo "$clash" | while IFS= read -r l; do echo -e "        ${RED}•${NC} ${l}"; done
                warn "Use [4] Edit to give each server unique ports."
            fi
        fi
        ((dirty)) && regenerate_all
        echo ""
        echo -ne "    ${G2}❯${NC} Press Enter..."
        read -r
    done
}

# ──────────────────── ADVANCED SETTINGS ────────────────────

advanced_settings() {
    show_logo
    if ! load_config; then
        err "Nothing installed yet. Run setup first."
        echo ""; echo -ne "    Press Enter..."; read -r; return
    fi

    echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
    echo -e "${G3}    │${NC}  ${G1}⚙${NC}  ${WHITE}${BOLD}ADVANCED SETTINGS${NC}                                        ${G3}│${NC}"
    echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
    echo ""
    printf "    ${DIM}current:${NC} mode=${CYAN}%s${NC} mtu=${CYAN}%s${NC} peers=${CYAN}%s${NC} dist=${CYAN}%s${NC} wd=${CYAN}%s${NC} shape=${CYAN}%s${NC}\n" \
        "$TUNNEL_MODE" "$TUNNEL_MTU" "$PEER_COUNT" "${BALANCE_MODE}" "$WATCHDOG" "${SHAPE_MBIT:-0}/${TUNING_PROFILE:-balanced}"
    info "Per-server settings (add/remove/ports/weight) live in menu ${WHITE}6${NC}."
    echo ""

    local input i
    echo -ne "    ${G2}❯${NC} Change transport mode? [y/${GREEN}N${NC}]: "
    read -r input
    if [[ "$input" == "y" || "$input" == "Y" ]]; then
        local old_mode="$TUNNEL_MODE"
        choose_mode
        if [[ "$TUNNEL_MODE" != "$old_mode" ]]; then
            # keys/ports are derived from the IP pairs, so both ends stay in sync
            local a b
            for ((i = 1; i <= PEER_COUNT; i++)); do
                if [[ "$ROLE" == "IRAN" ]]; then a="$IRAN_IP"; b="$(pget "$i" IP)"; else a="$IRAN_IP"; b="$SELF_IP"; fi
                pset "$i" KEY "$(peer_gre_key  "$a" "$b")"
                pset "$i" FOU "$(peer_fou_port "$a" "$b")"
            done
            IKE_PORT=$(derive_num "midos-ike|${IRAN_IP}" 20000 60000)
            if [[ "$TUNNEL_MODE" == "ipsec" ]]; then
                info "Installing strongswan..."
                install_deps
                write_ipsec_config
                local svc
                svc=$(ipsec_service_name)
                [[ -n "$svc" ]] && systemctl enable "$svc" > /dev/null 2>&1
            fi
            ensure_modules
            warn "EVERY other server must be switched to ${WHITE}${TUNNEL_MODE}${NC} as well."
        fi
    fi

    echo -ne "    ${G2}❯${NC} MTU [${CYAN}${TUNNEL_MTU}${NC}]: "
    read -r input
    if [[ -n "$input" ]]; then
        if [[ "$input" =~ ^[0-9]+$ ]] && ((input >= 1000 && input <= 1476)); then
            TUNNEL_MTU="$input"
        else
            warn "MTU must be 1000-1476; keeping ${TUNNEL_MTU}"
        fi
    fi

    SSH_PORT=$(detect_ssh_port)
    if [[ "$ROLE" == "IRAN" && "$BALANCE_MODE" != "ports" ]]; then
        echo -ne "    ${G2}❯${NC} TCP ports to keep local [${CYAN}${EXCLUDE_TCP_PORTS:-$SSH_PORT}${NC}]: "
        read -r input
        if [[ -n "$input" ]]; then
            if validate_ports "$input"; then EXCLUDE_TCP_PORTS="$input"; else warn "Invalid, unchanged"; fi
        else
            EXCLUDE_TCP_PORTS="${EXCLUDE_TCP_PORTS:-$SSH_PORT}"
        fi
        # SSH must always survive, whatever the user typed
        [[ ",${EXCLUDE_TCP_PORTS}," == *",${SSH_PORT},"* ]] || EXCLUDE_TCP_PORTS="${SSH_PORT},${EXCLUDE_TCP_PORTS}"

        echo -ne "    ${G2}❯${NC} UDP ports to keep local [${CYAN}${EXCLUDE_UDP_PORTS:-none}${NC}]: "
        read -r input
        if [[ -n "$input" ]]; then
            [[ "$input" == "none" ]] && input=""
            if validate_ports "$input"; then EXCLUDE_UDP_PORTS="$input"; else warn "Invalid, unchanged"; fi
        fi
    fi

    echo -ne "    ${G2}❯${NC} Watchdog auto-repair + failover? [${GREEN}Y${NC}/n]: "
    read -r input
    [[ "$input" == "n" || "$input" == "N" ]] && WATCHDOG=0 || WATCHDOG=1

    if [[ "$WATCHDOG" == "1" ]]; then
        echo -ne "    ${G2}❯${NC} Watchdog interval seconds [${CYAN}${WD_INTERVAL}${NC}]: "
        read -r input
        [[ "$input" =~ ^[0-9]+$ ]] && ((input >= 5)) && WD_INTERVAL="$input"
    fi

    echo ""
    echo -e "    ${DIM}Tuning profile: balanced | low_latency | throughput${NC}"
    echo -ne "    ${G2}❯${NC} Profile [${CYAN}${TUNING_PROFILE:-balanced}${NC}]: "
    read -r input
    case "$input" in
        balanced|low_latency|throughput) TUNING_PROFILE="$input" ;;
        "") : ;;
        *) err "Unknown profile - keeping ${TUNING_PROFILE:-balanced}" ;;
    esac

    echo -ne "    ${G2}❯${NC} TCP MSS inside the tunnel [${CYAN}${TCP_MSS:-0}${NC}] ${DIM}(0 = from MTU)${NC}: "
    read -r input
    [[ "$input" =~ ^[0-9]+$ ]] && TCP_MSS="$input"

    echo -ne "    ${G2}❯${NC} Per-tunnel shaping in Mbit [${CYAN}${SHAPE_MBIT:-0}${NC}] ${DIM}(0 = off)${NC}: "
    read -r input
    if [[ "$input" =~ ^[0-9]+$ ]]; then
        SHAPE_MBIT="$input"
        ((SHAPE_MBIT > 0)) && info "Set this ~15% below the measured speed of ONE tunnel."
    fi

    if [[ "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" ]]; then
        echo ""
        info "ESP keys and SPIs rotate on a schedule; ${WHITE}every server must use the same interval${NC}."
        echo -ne "    ${G2}❯${NC} ESP key rotation seconds [${CYAN}${ESP_ROTATE_SEC:-$DEF_ESP_ROTATE_SEC}${NC}] ${DIM}(0 = static keys)${NC}: "
        read -r input
        if [[ "$input" =~ ^[0-9]+$ ]]; then
            if ((input == 0 || input >= 600)); then ESP_ROTATE_SEC="$input"; else warn "Minimum is 600 s; unchanged"; fi
        fi
        info "Padding hides packet sizes from traffic analysis but costs bandwidth on small packets."
        echo -ne "    ${G2}❯${NC} ESP padding, pad packets up to N bytes [${CYAN}${ESP_TFC_PAD:-$DEF_ESP_TFC_PAD}${NC}] ${DIM}(0 = off, e.g. 256)${NC}: "
        read -r input
        if [[ "$input" =~ ^[0-9]+$ ]] && ((input <= 1400)); then ESP_TFC_PAD="$input"; fi
        rm -f "${CONFIG_DIR}/esp.master"      # re-derived on the next start
    fi

    regenerate_all
    ok "Configuration saved"

    echo ""
    echo -ne "    ${G2}❯${NC} Apply now (restart tunnels)? [${GREEN}Y${NC}/n]: "
    read -r input
    if [[ "$input" != "n" && "$input" != "N" ]]; then
        apply_now
        sleep 1
        ok "Applied"
        warn "Remember to apply the SAME mode/mtu on every other server."
    fi
    echo ""
    echo -ne "    ${G2}❯${NC} Press Enter..."
    read -r
}

# ──────────────────── STATUS ────────────────────

check_status() {
    show_logo
    echo -e "${G3}    ╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${G3}    ║${NC}  ${G1}📊${NC} ${WHITE}${BOLD}FULL DIAGNOSTICS${NC}                                          ${G3}║${NC}"
    echo -e "${G3}    ╚══════════════════════════════════════════════════════════════╝${NC}"
    echo ""

    local cfg=false i
    load_config && cfg=true

    # 1. Server
    echo -e "    ${G2}━━━ 1. SERVER ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    if $cfg; then
        local ri="🌍"
        [[ "$ROLE" == "IRAN" ]] && ri="🇮🇷"
        printf "    Role: ${CYAN}${BOLD}%-8s${NC} %s  Installed: ${DIM}%s${NC}\n" "$ROLE" "$ri" "$INSTALL_DATE"
        printf "    IRAN:   ${CYAN}%-16s${NC}  Mode: ${CYAN}%s${NC}  MTU ${CYAN}%s${NC}\n" "$IRAN_IP" "$(mode_label "$TUNNEL_MODE")" "$TUNNEL_MTU"
        if [[ "$ROLE" == "IRAN" ]]; then
            printf "    Peers:  ${CYAN}%s${NC} KHAREJ server(s)   Distribution: ${CYAN}%s${NC}\n" "$PEER_COUNT" "$(balance_label "$BALANCE_MODE")"
            if [[ "$BALANCE_MODE" != "ports" ]]; then
                printf "    Ports:  ${CYAN}ALL TCP+UDP${NC} tunneled   kept local: tcp ${GREEN}%s${NC} udp ${GREEN}%s${NC}\n" \
                    "${EXCLUDE_TCP_PORTS:-none}" "${EXCLUDE_UDP_PORTS:-none}"
            fi
        else
            printf "    Slot:   ${CYAN}%s${NC}  me ${CYAN}%s${NC} ↔ iran ${CYAN}%s${NC}  iface ${CYAN}%s${NC}\n" \
                "$PEER_INDEX" "$(pget 1 LOCAL)" "$(pget 1 REMOTE)" "$(pget 1 IFACE)"
        fi
        printf "    SSH:    ${GREEN}port %s protected${NC}\n" "${SSH_PORT:-22}"
    else
        warn "No config found"
    fi
    echo ""

    # 2. Modules
    echo -e "    ${G2}━━━ 2. KERNEL ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    lsmod | grep -q "^ip_gre" && ok "ip_gre: ${GREEN}LOADED${NC}" || warn "ip_gre: not listed"
    if [[ "$TUNNEL_MODE" == "fou" ]]; then
        lsmod | grep -q "^fou" && ok "fou: ${GREEN}LOADED${NC}" || err "fou: ${RED}MISSING${NC}"
        for ((i = 1; i <= PEER_COUNT; i++)); do
            ip fou show 2>/dev/null | grep -q "port $(pget "$i" FOU)" \
                && ok "FOU listener ${CYAN}$(pget "$i" FOU)${NC} ${DIM}($(pget "$i" NAME))${NC}" \
                || err "No FOU listener for $(pget "$i" NAME)"
        done
    fi
    local cc qd rpf
    cc=$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)
    [[ "$cc" == "bbr" ]] && ok "Congestion: ${GREEN}BBR${NC}" || warn "Congestion: ${cc}"
    qd=$(sysctl -n net.core.default_qdisc 2>/dev/null)
    ok "Default qdisc: ${CYAN}${qd}${NC}"
    rpf=$(sysctl -n net.ipv4.conf.all.rp_filter 2>/dev/null)
    [[ "$rpf" == "0" ]] && ok "rp_filter: ${GREEN}0 (correct)${NC}" || err "rp_filter=${rpf} ${RED}will drop tunnel traffic${NC}"
    echo ""

    # 3. Tunnels
    echo -e "    ${G2}━━━ 3. TUNNELS ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    local up_count=0 ifc tip mtu ls rx tx
    for ((i = 1; i <= PEER_COUNT; i++)); do
        ifc="$(pget "$i" IFACE)"
        echo -e "    ${G6}▸${NC} ${WHITE}$(pget "$i" NAME)${NC} ${DIM}($(pget "$i" IP)) → ${ifc}${NC}"
        if [[ "$(pget "$i" ENABLED)" != "1" ]]; then
            warn "  disabled by configuration"
            continue
        fi
        if ip link show "$ifc" > /dev/null 2>&1; then
            tip=$(ip -4 addr show "$ifc" 2>/dev/null | grep -oP 'inet \K[0-9./]+')
            mtu=$(ip link show "$ifc" | grep -oP 'mtu \K[0-9]+')
            ls=$(ip link show "$ifc" | grep -oP 'state \K\w+')
            rx=$(cat "/sys/class/net/${ifc}/statistics/rx_bytes" 2>/dev/null)
            tx=$(cat "/sys/class/net/${ifc}/statistics/tx_bytes" 2>/dev/null)
            if [[ "$ls" == "UP" || "$ls" == "UNKNOWN" ]]; then
                up_count=$((up_count + 1))
                ok "  ${GREEN}UP${NC}  ip ${CYAN}${tip}${NC} mtu ${CYAN}${mtu}${NC}  RX ${CYAN}$(numfmt --to=iec "${rx:-0}" 2>/dev/null || echo "$rx")${NC} TX ${CYAN}$(numfmt --to=iec "${tx:-0}" 2>/dev/null || echo "$tx")${NC}"
            else
                err "  state ${RED}${ls}${NC}"
            fi
            echo -e "      peer reachability: $(peer_health_label "$i")"
            # On IRAN, "up" is not the same as "carrying traffic": show whether
            # any DNAT rule actually points here. This is what tells the user at
            # a glance that all 4 servers are in use, not just tunnelled.
            if [[ "$ROLE" == "IRAN" ]]; then
                local dcount
                dcount=$(iptables -t nat -S "$MIDOS_PRE" 2>/dev/null | grep -c -- "--to-destination $(pget "$i" REMOTE)")
                if (( dcount > 0 )); then
                    local pt pu
                    pt="$(pget "$i" TCP)"; pu="$(pget "$i" UDP)"
                    case "$BALANCE_MODE" in
                        ports)   ok "  traffic: ${GREEN}YES${NC} ${DIM}tcp[${pt:--}] udp[${pu:--}]${NC}" ;;
                        balance) ok "  traffic: ${GREEN}YES${NC} ${DIM}balanced${NC}$([ -n "$pt" ] && echo " ${DIM}+ pinned[${pt}]${NC}")" ;;
                        *)       ok "  traffic: ${GREEN}YES${NC} ${DIM}(${dcount} DNAT rule(s))${NC}" ;;
                    esac
                else
                    warn "  traffic: ${RED}NONE${NC} - no ports routed here (this server is idle)"
                fi
            fi
        else
            err "  interface ${ifc} NOT FOUND"
        fi
    done
    ((PEER_COUNT > 0)) && info "${up_count}/${PEER_COUNT} tunnel(s) up"
    if [[ "$ROLE" == "IRAN" && "$BALANCE_MODE" == "ports" ]]; then
        local clash; clash="$(ports_collision)"
        if [[ -n "$clash" ]]; then
            echo ""
            err "PORT CLASH - these ports are on more than one server, only the first gets them:"
            echo "$clash" | while IFS= read -r l; do echo -e "        ${RED}•${NC} ${l}"; done
            warn "Fix in menu 6 → Edit so every server has unique ports."
        fi
    fi
    echo ""

    # 4. Services
    echo -e "    ${G2}━━━ 4. SERVICES ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    if [[ -f "$SERVICE_FILE" ]]; then
        systemctl is-active --quiet Midos-tunnel.service && ok "Midos-tunnel: ${GREEN}● ACTIVE${NC}" || err "Midos-tunnel: ${RED}● INACTIVE${NC}"
        systemctl is-enabled --quiet Midos-tunnel.service 2>/dev/null && ok "Boot: ${GREEN}ON${NC}" || warn "Boot: OFF"
    else
        err "Midos-tunnel.service not installed"
    fi
    if [[ -f "$WD_SERVICE_FILE" ]]; then
        systemctl is-active --quiet Midos-tunnel-watchdog.service && ok "watchdog: ${GREEN}● ACTIVE${NC}" || warn "watchdog: OFF"
    fi
    if [[ "$TUNNEL_MODE" == "ipsec" ]]; then
        local sa
        sa=$(ipsec status 2>/dev/null | grep -c "ESTABLISHED")
        (( sa > 0 )) && ok "IPsec SA: ${GREEN}${sa} ESTABLISHED${NC}" || err "IPsec SA: ${RED}DOWN${NC} ${DIM}(check PSK + port ${IKE_PORT})${NC}"
    fi
    if [[ "$TUNNEL_MODE" == "esp" || "$TUNNEL_MODE" == "espudp" ]]; then
        local st po want rot ep now left
        st=$(ip xfrm state show 2>/dev/null | grep -c "^src")
        po=$(ip xfrm policy show 2>/dev/null | grep -c "^src")
        rot="${ESP_ROTATE_SEC:-$DEF_ESP_ROTATE_SEC}"
        # rotating: 5 inbound (window) + 1 outbound per peer; static: 1 + 1
        (( rot > 0 )) && want=$((PEER_COUNT * 6)) || want=$((PEER_COUNT * 2))
        if (( st >= want && po >= PEER_COUNT * 3 )); then
            ok "ESP: ${GREEN}ACTIVE${NC} ${DIM}(${st} SAs, ${po} policies, AES-256-GCM)${NC}"
        else
            err "ESP: ${RED}INCOMPLETE${NC} ${DIM}(${st}/${want} SAs, ${po} policies - some traffic is in the clear!)${NC}"
        fi
        if (( rot > 0 )); then
            now=$(date +%s); ep=$(( now / rot )); left=$(( (ep + 1) * rot - now ))
            info "Key epoch ${WHITE}${ep}${NC}, next rotation in ${WHITE}$((left / 60)) min${NC}  ${DIM}(both servers' clocks must agree within $((2 * rot / 60)) min - check: date -u)${NC}"
            systemctl is-active --quiet Midos-tunnel-rekey.timer && ok "Rekey timer: ${GREEN}ACTIVE${NC}" || err "Rekey timer: ${RED}NOT RUNNING${NC} ${DIM}(the peer will drop this side in ~$((2 * rot / 60)) min)${NC}"
        else
            warn "ESP key rotation is OFF (static keys) - turn it on in Advanced settings on every server."
        fi
    fi
    echo ""

    # 5. Firewall
    echo -e "    ${G2}━━━ 5. FIREWALL ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    [[ "$(cat /proc/sys/net/ipv4/ip_forward 2>/dev/null)" == "1" ]] && ok "ip_forward: ${GREEN}ON${NC}" || err "ip_forward: ${RED}OFF${NC}"
    if iptables -t nat -L "$MIDOS_PRE" -n > /dev/null 2>&1; then
        local dn
        dn=$(iptables -t nat -S "$MIDOS_PRE" 2>/dev/null | grep -c DNAT)
        ok "MIDOS_PRE: ${GREEN}OK${NC} ${DIM}(${dn} DNAT rules)${NC}"
        if [[ "$ROLE" == "IRAN" ]]; then
            iptables -t nat -S "$MIDOS_PRE" 2>/dev/null | grep DNAT | while read -r l; do
                echo -e "      ${DIM}${l}${NC}"
            done
        fi
    else
        [[ "$ROLE" == "IRAN" ]] && err "MIDOS_PRE missing" || info "MIDOS_PRE not used on KHAREJ"
    fi
    iptables -t nat -L "$MIDOS_POST" -n > /dev/null 2>&1 && ok "MIDOS_POST: ${GREEN}OK${NC}" || err "MIDOS_POST missing"
    iptables -t mangle -L MIDOS_MSS -n > /dev/null 2>&1 && ok "MSS clamp: ${GREEN}$((TUNNEL_MTU - 40))${NC}" || warn "MSS clamp missing"
    if [[ -f "$HEALTH_FILE" ]]; then
        echo -e "    ${DIM}watchdog health file:${NC}"
        while read -r hi hs; do
            [[ -z "$hi" ]] && continue
            [[ "$hs" == "up" ]] && echo -e "      ${GREEN}✔${NC} peer ${hi} $(pget "$hi" NAME): up" \
                                || echo -e "      ${RED}✘${NC} peer ${hi} $(pget "$hi" NAME): down"
        done < "$HEALTH_FILE"
    fi
    echo ""

    # 6. Connectivity
    echo -e "    ${G2}━━━ 6. CONNECTIVITY ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    if $cfg && ((PEER_COUNT > 0)); then
        animated_ping "$(pget 1 LOCAL)" "TEST 1: Local tunnel IP"
        for ((i = 1; i <= PEER_COUNT; i++)); do
            [[ "$(pget "$i" ENABLED)" == "1" ]] || continue
            animated_ping "$(pget "$i" REMOTE)" "PEER: $(pget "$i" NAME) through tunnel" "$(pget "$i" IFACE)"
        done
        animated_ping "8.8.8.8" "TEST: Internet"
    else
        animated_ping "8.8.8.8" "Internet"
    fi

    # 7. Health
    echo -e "    ${G2}━━━ 7. HEALTH ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    local tot=0 pass=0
    chk() {
        tot=$((tot + 1))
        if eval "$2" > /dev/null 2>&1; then
            pass=$((pass + 1)); printf "    ${GREEN}✔${NC} %-14s ${GREEN}OK${NC}\n" "$1"
        else
            printf "    ${RED}✘${NC} %-14s ${RED}FAIL${NC}\n" "$1"
        fi
    }
    chk "MOI"       "lsmod | grep -q '^ip_gre' || ip link show $(pget 1 IFACE)"
    chk "Service"   "systemctl is-active --quiet Midos-tunnel.service"
    chk "Forward"   "[[ \$(cat /proc/sys/net/ipv4/ip_forward) == 1 ]]"
    chk "rp_filter" "[[ \$(sysctl -n net.ipv4.conf.all.rp_filter) == 0 ]]"
    chk "BBR"       "[[ \$(sysctl -n net.ipv4.tcp_congestion_control) == bbr ]]"
    for ((i = 1; i <= PEER_COUNT; i++)); do
        [[ "$(pget "$i" ENABLED)" == "1" ]] || continue
        chk "$(pget "$i" NAME)" "ping -c1 -W3 -I $(pget "$i" IFACE) $(pget "$i" REMOTE)"
    done

    echo ""
    ((tot == 0)) && tot=1
    local pct=$((pass * 100 / tot))
    local hc="${RED}" ht="CRITICAL"
    ((pct >= 100)) && { hc="${GREEN}"; ht="PERFECT"; }
    ((pct >= 80 && pct < 100)) && { hc="${GREEN}"; ht="HEALTHY"; }
    ((pct >= 60 && pct < 80))  && { hc="${YELLOW}"; ht="GOOD"; }
    ((pct >= 40 && pct < 60))  && { hc="${YELLOW}"; ht="DEGRADED"; }
    echo -e "    ${hc}${BOLD}  ⬤ ${ht}  ${pass}/${tot} (${pct}%)${NC}"

    if ((pass < tot)); then
        echo ""
        info "Troubleshooting hints:"
        echo -e "    ${DIM}• Peer FAIL  → that KHAREJ box must be installed with the SAME mode AND its own slot number${NC}"
        echo -e "    ${DIM}• Peer FAIL  → the provider may block MOI; switch mode to FOU or IPSEC (menu 5)${NC}"
        echo -e "    ${DIM}• In failover mode a dead peer is bypassed automatically within ~1 minute${NC}"
        echo -e "    ${DIM}• Log file   → ${LOG_FILE}${NC}"
    fi
    echo ""
    echo -ne "    ${G2}❯${NC} Press Enter..."
    read -r
}

# ──────────────────── RESTART ────────────────────

restart_tunnel() {
    local i
    show_logo
    echo -e "    ${G1}🔄${NC} ${WHITE}${BOLD}RESTARTING ALL TUNNELS${NC}"
    echo ""
    if [[ ! -f "$UP_SCRIPT" ]]; then
        err "Not installed"
        echo ""; echo -ne "    Press Enter..."; read -r; return
    fi
    load_config

    stp "1/4" "Modules..."
    ensure_modules
    ok "OK"

    stp "2/4" "Stopping..."
    bash "$DOWN_SCRIPT" > /dev/null 2>&1
    sleep 1
    ok "Stopped"

    stp "3/4" "Starting ${PEER_COUNT} tunnel(s)..."
    rm -f "$HEALTH_FILE"
    apply_now
    sleep 2

    stp "4/4" "Verifying..."
    for ((i = 1; i <= PEER_COUNT; i++)); do
        if ip link show "$(pget "$i" IFACE)" 2>/dev/null | grep -qE "UP|UNKNOWN"; then
            ok "$(pget "$i" NAME): ${GREEN}${BOLD}UP${NC} ${DIM}($(pget "$i" IFACE) → $(pget "$i" IP))${NC}"
        else
            err "$(pget "$i" NAME): not up - see ${CYAN}${LOG_FILE}${NC}"
        fi
    done

    echo ""
    ((PEER_COUNT > 0)) && animated_ping "$(pget 1 LOCAL)" "Local → $(pget 1 LOCAL)"
    for ((i = 1; i <= PEER_COUNT; i++)); do
        [[ "$(pget "$i" ENABLED)" == "1" ]] || continue
        animated_ping "$(pget "$i" REMOTE)" "$(pget "$i" NAME) → $(pget "$i" REMOTE)" "$(pget "$i" IFACE)"
    done
    echo -ne "    ${G2}❯${NC} Press Enter..."
    read -r
}

# ──────────────────── LIVE MONITOR ────────────────────

live_monitor() {
    load_config
    if ((PEER_COUNT == 0)); then
        show_logo; err "Nothing configured"; echo ""; echo -ne "    Press Enter..."; read -r; return
    fi

    local i ifc
    declare -A PRX PTX
    for ((i = 1; i <= PEER_COUNT; i++)); do
        ifc="$(pget "$i" IFACE)"
        PRX[$i]=$(cat "/sys/class/net/${ifc}/statistics/rx_bytes" 2>/dev/null || echo 0)
        PTX[$i]=$(cat "/sys/class/net/${ifc}/statistics/tx_bytes" 2>/dev/null || echo 0)
    done

    while true; do
        show_logo
        echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
        echo -e "${G3}    │${NC}  ${G1}📡${NC} ${WHITE}${BOLD}LIVE MONITOR${NC}   ${DIM}Ctrl+C to exit${NC}                          ${G3}│${NC}"
        echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
        echo ""
        printf "    ${DIM}%-14s %-10s %10s %10s %9s %10s${NC}\n" "SERVER" "IFACE" "DOWN" "UP" "RTT" "TOTAL RX"
        sleep 1
        local crx ctx rxr txr rtt trx=0 ttx=0
        for ((i = 1; i <= PEER_COUNT; i++)); do
            ifc="$(pget "$i" IFACE)"
            if ! ip link show "$ifc" > /dev/null 2>&1; then
                printf "    %-14s %-10s ${RED}%31s${NC}\n" "$(pget "$i" NAME)" "$ifc" "interface down"
                continue
            fi
            crx=$(cat "/sys/class/net/${ifc}/statistics/rx_bytes" 2>/dev/null || echo 0)
            ctx=$(cat "/sys/class/net/${ifc}/statistics/tx_bytes" 2>/dev/null || echo 0)
            rxr=$(( (crx - ${PRX[$i]:-0}) * 8 / 1000 ))
            txr=$(( (ctx - ${PTX[$i]:-0}) * 8 / 1000 ))
            PRX[$i]=$crx; PTX[$i]=$ctx
            trx=$((trx + rxr)); ttx=$((ttx + txr))
            rtt=$(ping -c 1 -W 2 -I "$ifc" "$(pget "$i" REMOTE)" 2>/dev/null | grep -oP 'time=\K[0-9.]+' | head -1)
            [[ -z "$rtt" ]] && rtt="---"
            printf "    %-14s ${CYAN}%-10s${NC} ${GREEN}%7s kb${NC} ${GREEN}%7s kb${NC} ${G2}%6s ms${NC} ${CYAN}%10s${NC}\n" \
                "$(pget "$i" NAME)" "$ifc" "$rxr" "$txr" "$rtt" \
                "$(numfmt --to=iec "$crx" 2>/dev/null || echo "$crx")"
        done
        echo ""
        printf "    ${WHITE}%-14s${NC} %-10s ${GREEN}%7s kb${NC} ${GREEN}%7s kb${NC}\n" "TOTAL" "" "$trx" "$ttx"
        echo ""
        printf "    Conntrack : ${CYAN}%s${NC} / %s\n" \
            "$(cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null)" \
            "$(cat /proc/sys/net/netfilter/nf_conntrack_max 2>/dev/null)"
        if [[ "$ROLE" == "IRAN" && "$BALANCE_MODE" == "failover" ]]; then
            local act
            act=$(iptables -t nat -S "$MIDOS_PRE" 2>/dev/null | grep -m1 -oP 'to-destination \K[0-9.]+')
            [[ -n "$act" ]] && printf "    Active peer: ${G2}%s${NC}\n" "$act"
        fi
        echo ""
        echo -e "    ${DIM}refreshing every second...${NC}"
        sleep 1
    done
}
# ──────────────────── BACKUP PATH (camouflaged relay) ────────────────────
#
# The kernel transports are the fast path. Where a network drops them outright -
# a bare IP protocol, or any sustained UDP flow to one foreign address - no
# amount of tuning helps. This is the fallback for that case: the same ports are
# relayed inside a TLS session shaped like ordinary browser traffic, which costs
# CPU and latency because every byte crosses user space. It is meant to be
# switched on when the fast path is blocked, not to replace it.

veil_arch() {
    case "$(uname -m)" in
        aarch64 | arm64) echo "midos-tunnel-arm64" ;;
        *) echo "midos-tunnel" ;;
    esac
}

# Both ends already share IPSEC_PSK, so deriving from it means the operator
# never has to copy a second secret between servers - the same reasoning behind
# the derived MOI keys and transport ports.
veil_psk() {
    # the hash inputs keep their original names: both ends derive the same key and
    # port from them, so renaming would split servers on different versions
    printf '%s' "midos-veil|${IPSEC_PSK}" | sha256sum | cut -c1-48
}

veil_server_port() { derive_num "midos-veil-port|${IRAN_IP}" 10000 60000; }

veil_installed() { [[ -x "$VEIL_BIN" ]]; }
veil_active() { systemctl is-active --quiet Midos-tunnel-veil.service 2>/dev/null; }

veil_fetch() {
    local base="$DOWNLOAD_BASE" name tmp
    name="$(veil_arch)"
    tmp="${CONFIG_DIR}/.veil.download"
    mkdir -p "$CONFIG_DIR"
    info "Downloading the backup-path helper (${name})..."
    if ! curl --compressed -fsSL --connect-timeout 20 --retry 4 --retry-delay 3 \
        -m 300 "${base%/}/${name}" -o "$tmp" 2> /dev/null; then
        rm -f "$tmp"
        err "Could not download ${name} from ${base}"
        return 1
    fi
    # Refuse anything that is not a Linux binary. A captive portal or a stray
    # HTML error page would otherwise be installed and only fail at runtime.
    if [[ "$(head -c 4 "$tmp" | od -An -tx1 | tr -d ' \n')" != "7f454c46" ]]; then
        rm -f "$tmp"
        err "Downloaded file is not a Linux binary - aborting."
        return 1
    fi
    VEIL_BIN="/usr/local/sbin/midos-tunnel"
    mv -f "$tmp" "$VEIL_BIN"
    chmod 755 "$VEIL_BIN"
    # an older install moves to the current names; its old path becomes a link
    "$VEIL_BIN" migrate > /dev/null 2>&1 || true
    ok "Helper installed at ${CYAN}${VEIL_BIN}${NC}"
    return 0
}

# The two ends must relay the same ports, so we reuse the list the kernel path
# already carries instead of asking for it twice.
veil_port_list() {
    local i out="" spec item
    for ((i = 1; i <= PEER_COUNT; i++)); do
        spec="$(pget "$i" TCP)"
        [[ -z "$spec" ]] && continue
        local IFS=','
        local items=($spec)
        unset IFS
        for item in "${items[@]}"; do
            [[ "$item" == *-* ]] && continue # ranges are not relayed here
            item="${item%%:*}"               # a remap entry keeps its external port
            out+="${item} "
        done
    done
    echo "$out" | tr ' ' '\n' | grep -v '^$' | sort -un | tr '\n' ' '
}

write_veil_config() {
    local mode="$1" ports_json="" p first=1
    mkdir -p "$CONFIG_DIR"
    for p in $(veil_port_list); do
        ((first)) || ports_json+=", "
        ports_json+="\"${p}\""
        first=0
    done
    if [[ "$mode" == "server" ]]; then
        cat > "$VEIL_CONF" << EOF
{
  "mode": "server",
  "psk": "$(veil_psk)",
  "listen": "0.0.0.0:${VEIL_PORT}",
  "server_name": "${VEIL_SNI}",
  "targets": "127.0.0.1",
  "ports": [${ports_json}]
}
EOF
    else
        cat > "$VEIL_CONF" << EOF
{
  "mode": "client",
  "psk": "$(veil_psk)",
  "server": "${VEIL_PEER}:${VEIL_PORT}",
  "server_name": "${VEIL_SNI}",
  "fingerprint": "${VEIL_FP}",
  "insecure_tls": true,
  "carriers": 2,
  "ports": [${ports_json}]
}
EOF
    fi
    chmod 600 "$VEIL_CONF"
}

write_veil_service() {
    cat > "$VEIL_SERVICE_FILE" << EOF
[Unit]
Description=Midos Tunnel backup path (camouflaged relay)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${VEIL_BIN} -config ${VEIL_CONF}
Restart=always
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=${CONFIG_DIR}
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload > /dev/null 2>&1
}

veil_menu() {
    local input
    while true; do
        show_logo
        echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
        echo -e "${G3}    │${NC}  ${G1}🕶${NC}  ${WHITE}${BOLD}BACKUP PATH${NC} ${DIM}(for when the fast path is blocked)${NC}      ${G3}│${NC}"
        echo -e "${G3}    ├──────────────────────────────────────────────────────────────┤${NC}"
        printf "    ${G3}│${NC}  helper : %-50s${G3}│${NC}\n" "$(veil_installed && echo "installed" || echo "not installed")"
        printf "    ${G3}│${NC}  service: %-50s${G3}│${NC}\n" "$(veil_active && echo "RUNNING" || echo "stopped")"
        printf "    ${G3}│${NC}  role   : %-50s${G3}│${NC}\n" "$([[ "$ROLE" == "IRAN" ]] && echo "client (dials abroad)" || echo "server (accepts)")"
        echo -e "${G3}    ├──────────────────────────────────────────────────────────────┤${NC}"
        echo -e "${G3}    │${NC}   ${G2}[1]${NC} ➤ Enable / reconfigure                                ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[2]${NC} ➤ Disable                                             ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[3]${NC} ➤ Status and recent log                               ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${RED}[0]${NC} ➤ Back                                                ${G3}│${NC}"
        echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
        echo ""
        echo -ne "    ${G2}❯${NC} Choice: "
        read -r input
        case "$input" in
            1) veil_enable ;;
            2) veil_disable ;;
            3) veil_status ;;
            0) return ;;
            *)
                err "Invalid!"
                sleep 1
                ;;
        esac
    done
}

veil_enable() {
    local input ports
    echo ""
    if [[ ! -f "$CONFIG_FILE" ]]; then
        err "Set the tunnel up first - the backup path reuses its peers and ports."
        echo ""
        echo -ne "    Press Enter..."
        read -r
        return
    fi
    load_config

    ports="$(veil_port_list)"
    if [[ -z "$ports" ]]; then
        err "No per-peer ports are configured, so there is nothing to relay."
        info "Choose distribution mode ${WHITE}PORTS${NC} and give each server its ports first."
        echo ""
        echo -ne "    Press Enter..."
        read -r
        return
    fi

    VEIL_PORT="${VEIL_PORT:-$(veil_server_port)}"
    VEIL_SNI="${VEIL_SNI:-www.cloudflare.com}"
    VEIL_FP="${VEIL_FP:-chrome}"

    echo -ne "    ${G2}❯${NC} TLS port [${CYAN}${VEIL_PORT}${NC}]: "
    read -r input
    [[ "$input" =~ ^[0-9]+$ ]] && VEIL_PORT="$input"
    echo -ne "    ${G2}❯${NC} Name presented in the handshake [${CYAN}${VEIL_SNI}${NC}]: "
    read -r input
    [[ -n "$input" ]] && VEIL_SNI="$input"

    if [[ "$ROLE" == "IRAN" ]]; then
        echo -e "    ${DIM}Browser to imitate: chrome | firefox | safari | edge | random${NC}"
        echo -ne "    ${G2}❯${NC} Fingerprint [${CYAN}${VEIL_FP}${NC}]: "
        read -r input
        case "$input" in
            chrome | firefox | safari | edge | random) VEIL_FP="$input" ;;
        esac
        VEIL_PEER="$(pget 1 IP)"
        echo -ne "    ${G2}❯${NC} Which server abroad [${CYAN}${VEIL_PEER}${NC}]: "
        read -r input
        [[ -n "$input" ]] && VEIL_PEER="$input"
        [[ -z "$VEIL_PEER" ]] && {
            err "No peer to dial."
            sleep 2
            return
        }
    fi

    veil_installed || veil_fetch || {
        echo ""
        echo -ne "    Press Enter..."
        read -r
        return
    }

    if [[ "$ROLE" == "IRAN" ]]; then write_veil_config client; else write_veil_config server; fi
    if ! "$VEIL_BIN" -config "$VEIL_CONF" -check > /dev/null 2>&1; then
        err "The helper rejected the generated configuration:"
        "$VEIL_BIN" -config "$VEIL_CONF" -check 2>&1 | sed 's/^/      /'
        echo ""
        echo -ne "    Press Enter..."
        read -r
        return
    fi
    write_veil_service
    systemctl enable Midos-tunnel-veil.service > /dev/null 2>&1
    systemctl restart Midos-tunnel-veil.service
    sleep 2

    # let the peer reach our TLS port through our own INPUT chain
    if [[ "$ROLE" != "IRAN" ]]; then
        iptables -C "$MIDOS_IN" -p tcp --dport "$VEIL_PORT" -j ACCEPT 2> /dev/null \
            || iptables -I "$MIDOS_IN" 1 -p tcp --dport "$VEIL_PORT" -j ACCEPT 2> /dev/null
    fi

    echo ""
    if veil_active; then
        ok "Backup path ${GREEN}RUNNING${NC} on port ${CYAN}${VEIL_PORT}${NC}"
        info "Relaying: ${CYAN}${ports}${NC}"
        [[ "$ROLE" == "IRAN" ]] && info "Imitating ${CYAN}${VEIL_FP}${NC} towards ${CYAN}${VEIL_SNI}${NC}"
        warn "Enable it on BOTH ends - the key is derived from the tunnel PSK, so"
        warn "both servers must already share the same tunnel passphrase."
    else
        err "It did not stay up:"
        journalctl -u Midos-tunnel-veil -n 12 --no-pager 2> /dev/null | sed 's/^/      /'
    fi
    save_config
    echo ""
    echo -ne "    Press Enter..."
    read -r
}

veil_disable() {
    echo ""
    systemctl disable --now Midos-tunnel-veil.service > /dev/null 2>&1
    ok "Backup path stopped and disabled. The helper and its config are kept."
    echo ""
    echo -ne "    Press Enter..."
    read -r
}

veil_status() {
    echo ""
    printf "    helper    : %s\n" "$(veil_installed && "$VEIL_BIN" -version 2>&1 | tail -1 || echo "not installed")"
    printf "    service   : %s\n" "$(systemctl is-active Midos-tunnel-veil 2> /dev/null || echo inactive)"
    if [[ -f "$VEIL_CONF" ]]; then
        printf "    mode      : %s\n" "$(grep -oP '"mode"\s*:\s*"\K[a-z]+' "$VEIL_CONF")"
        printf "    ports     : %s\n" "$(grep -oP '"ports"\s*:\s*\[\K[^]]*' "$VEIL_CONF" | tr -d '" ')"
        printf "    sockets   : %s\n" "$(ss -tlnp 2> /dev/null | grep -cE 'midos-(tunnel|veil)')"
    else
        printf "    config    : none\n"
    fi
    echo ""
    journalctl -u Midos-tunnel-veil -n 12 --no-pager 2> /dev/null | sed 's/^/    /'
    echo ""
    echo -ne "    Press Enter..."
    read -r
}

# ──────────────────── WEB PANEL ────────────────────
# The web panel lives in the same helper binary (version 2.0.0 and later). It
# runs as its own service and changes tunnels only through systemd, so turning
# it off or removing it never touches a running tunnel.

PANEL_CONF="${CONFIG_DIR}/panel.json"
PANEL_UNIT="midos-tunnel-panel.service"

panel_pause() {
    echo ""
    echo -ne "    Press Enter..."
    read -r
}

# panel_helper_ready: the installed helper is new enough to carry the panel
panel_helper_ready() {
    local v=""
    veil_installed && v=$("$VEIL_BIN" -version 2>&1 | awk '{print $2}')
    [[ -n "$v" && "$(printf '%s\n%s\n' "$v" "2.0.0" | sort -V | head -1)" == "2.0.0" ]]
}

panel_open_port() {
    local port
    port=$(grep -oP '"listen":\s*"[^"]*:\K[0-9]+' "$PANEL_CONF" 2> /dev/null)
    [[ -z "$port" ]] && return
    if iptables -S "$MIDOS_IN" > /dev/null 2>&1; then
        iptables -C "$MIDOS_IN" -p tcp --dport "$port" -j ACCEPT 2> /dev/null \
            || iptables -I "$MIDOS_IN" 1 -p tcp --dport "$port" -j ACCEPT 2> /dev/null
    fi
    if command -v ufw > /dev/null 2>&1 && ufw status 2> /dev/null | grep -q "Status: active"; then
        ufw allow "${port}/tcp" > /dev/null 2>&1
    fi
}

# prepare_kernel_base installs everything a kernel tunnel needs short of the
# tunnel itself - the installer at the path the panel drives, the kernel modules,
# the packages and the sysctl tuning - so the panel's Kernel page is ready to
# build an ESP/MOI tunnel. The final build still happens from the menu or the
# panel; this only lays the groundwork.
KERNEL_SELF="/usr/local/sbin/Midos-tunnel.sh"
prepare_kernel_base() {
    info "Preparing the kernel-tunnel base (prerequisites for ESP/MOI)..."
    if [[ "$(readlink -f "$0" 2> /dev/null)" != "$KERNEL_SELF" ]]; then
        [[ -f "$0" ]] && cp -f "$0" "$KERNEL_SELF" 2> /dev/null && chmod +x "$KERNEL_SELF"
    fi
    if [[ ! -f "$KERNEL_SELF" ]]; then
        local base="$DOWNLOAD_BASE"
        curl -fsSL "${base}/Midos-tunnel.sh" -o "$KERNEL_SELF" 2> /dev/null && chmod +x "$KERNEL_SELF"
    fi
    [[ -f "$KERNEL_SELF" ]] && ok "Installer ready at ${CYAN}${KERNEL_SELF}${NC}" \
        || warn "Could not place the installer at ${KERNEL_SELF}; building a kernel tunnel from the panel may be unavailable."
    ensure_modules
    install_deps > /dev/null 2>&1 || true
    write_sysctl
    mkdir -p "$CONFIG_DIR"
    touch "${CONFIG_DIR}/kernel-base-ready"
    ok "Kernel prerequisites installed (modules, packages, BBR + forwarding). Build from the Kernel page."
}

panel_install() {
    echo ""
    if ! panel_helper_ready; then
        veil_fetch || { panel_pause; return; }
        if ! panel_helper_ready; then
            err "The downloaded helper is older than 2.0.0 and has no web panel."
            panel_pause
            return
        fi
    fi
    if [[ -f "$PANEL_CONF" ]]; then
        systemctl enable --now "$PANEL_UNIT" > /dev/null 2>&1
        ok "The panel is already set up and running"
        info "Address: ${CYAN}$("$VEIL_BIN" panel url -config "$PANEL_CONF")${NC}"
        info "Forgot the password? Use ${WHITE}[3]${NC}"
    elif "$VEIL_BIN" panel init -config "$PANEL_CONF"; then
        warn "Write the password down now: it is stored only as a hash."
    else
        err "Panel setup failed"
        panel_pause
        return
    fi
    panel_open_port
    prepare_kernel_base
    panel_pause
}

panel_remove() {
    echo ""
    echo -ne "    ${YELLOW}❯${NC} Remove the web panel? Tunnels keep running. [y/${GREEN}N${NC}]: "
    local a
    read -r a
    [[ "${a,,}" == "y" || "${a,,}" == "yes" ]] || return
    systemctl disable --now "$PANEL_UNIT" > /dev/null 2>&1
    rm -f "/etc/systemd/system/${PANEL_UNIT}" "$PANEL_CONF" "${CONFIG_DIR}/panel.crt" "${CONFIG_DIR}/panel.key"
    systemctl daemon-reload > /dev/null 2>&1
    ok "Web panel removed (tunnels untouched)"
    panel_pause
}

panel_menu() {
    local input addr state
    while true; do
        show_logo
        state="not installed"
        if [[ -f "$PANEL_CONF" ]]; then
            systemctl is-active --quiet "$PANEL_UNIT" 2> /dev/null && state="RUNNING" || state="stopped"
        fi
        addr="-"
        [[ -f "$PANEL_CONF" ]] && panel_helper_ready && addr=$("$VEIL_BIN" panel url -config "$PANEL_CONF" 2> /dev/null)
        echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
        echo -e "${G3}    │${NC}  ${G1}🖥${NC}  ${WHITE}${BOLD}WEB PANEL${NC} ${DIM}(browser dashboard and tunnel management)${NC}  ${G3}│${NC}"
        echo -e "${G3}    ├──────────────────────────────────────────────────────────────┤${NC}"
        printf "    ${G3}│${NC}  panel  : %-50s${G3}│${NC}\n" "$state"
        printf "    ${G3}│${NC}  address: %-50s${G3}│${NC}\n" "${addr:0:50}"
        echo -e "${G3}    ├──────────────────────────────────────────────────────────────┤${NC}"
        echo -e "${G3}    │${NC}   ${G2}[1]${NC} ➤ Install / start                                      ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[2]${NC} ➤ Show address                                         ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[3]${NC} ➤ Reset password                                       ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[4]${NC} ➤ Turn off two-factor login                            ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${G2}[5]${NC} ➤ ${RED}Remove panel${NC} ${DIM}(tunnels keep running)${NC}                  ${G3}│${NC}"
        echo -e "${G3}    │${NC}   ${RED}[0]${NC} ➤ Back                                                 ${G3}│${NC}"
        echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
        echo ""
        echo -ne "    ${G2}❯${NC} Choice: "
        read -r input
        case "$input" in
            1) panel_install ;;
            2)
                echo ""
                if [[ -f "$PANEL_CONF" ]]; then info "Address: ${CYAN}${addr}${NC}"; else err "The panel is not installed yet."; fi
                panel_pause
                ;;
            3)
                echo ""
                if [[ -f "$PANEL_CONF" ]] && "$VEIL_BIN" panel passwd -config "$PANEL_CONF"; then
                    systemctl restart "$PANEL_UNIT" > /dev/null 2>&1
                else
                    err "The panel is not installed yet."
                fi
                panel_pause
                ;;
            4)
                echo ""
                if [[ -f "$PANEL_CONF" ]] && "$VEIL_BIN" panel 2fa-off -config "$PANEL_CONF"; then
                    systemctl restart "$PANEL_UNIT" > /dev/null 2>&1
                else
                    err "The panel is not installed yet."
                fi
                panel_pause
                ;;
            5) panel_remove ;;
            0) return ;;
            *)
                err "Invalid!"
                sleep 1
                ;;
        esac
    done
}

# ──────────────────── UNINSTALL ────────────────────

uninstall_tunnel() {
    show_logo
    echo -e "${RED}    ╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${RED}    ║${NC}  ${RED}💣${NC} ${WHITE}${BOLD}NUCLEAR UNINSTALL${NC}                                        ${RED}║${NC}"
    echo -e "${RED}    ╚══════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "    ${RED}Will COMPLETELY remove:${NC}"
    echo -e "    ${DIM}  • All MOI/FOU tunnels & interfaces${NC}"
    echo -e "    ${DIM}  • All services, timers, scripts, watchdog${NC}"
    echo -e "    ${DIM}  • Our own iptables chains (MIDOS_*) and their hooks${NC}"
    echo -e "    ${DIM}  • IPsec/ESP state, sysctl tuning, MOI modules${NC}"
    echo ""
    echo -e "    ${GREEN}  ✔ SSH stays working${NC}"
    echo -e "    ${GREEN}  ✔ x-ui / xray / Docker / other firewall rules are NOT touched${NC}"
    echo ""
    echo -ne "    ${RED}❯${NC} Confirm? [${RED}y${NC}/${GREEN}n${NC}]: "
    read -r confirm
    confirm=$(echo "$confirm" | tr '[:upper:]' '[:lower:]' | xargs)
    if [[ "$confirm" != "y" && "$confirm" != "yes" ]]; then
        info "Cancelled."; echo ""; echo -ne "    Press Enter..."; read -r; return
    fi

    load_config

    echo ""
    echo -e "    ${RED}${BOLD}━━━ NUCLEAR UNINSTALL STARTED ━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""

    stp "1/9" "Stopping services..."
    for svc in Midos-tunnel-watchdog Midos-tunnel Midos-tunnel-encap Midos-tunnel-veil midos-tunnel-watchdog midos-tunnel midos-gre greKH greIR; do
        systemctl stop "${svc}.service" 2>/dev/null
        systemctl disable "${svc}.service" 2>/dev/null
    done
    systemctl disable --now Midos-tunnel-rekey.timer 2>/dev/null
    systemctl stop Midos-tunnel-rekey.service 2>/dev/null
    # the web panel, and every relay it or the backup-path menu created
    systemctl disable --now midos-tunnel-panel.service midos-veil-panel.service 2>/dev/null
    local relay
    for relay in $(systemctl list-units --all --plain --no-legend 'midos-tunnel@*' 'midos-veil@*' 'Midos-tunnel-veil*' 2>/dev/null | awk '{print $1}') \
        $(ls "$CONFIG_DIR"/tunnels/*.json 2>/dev/null | sed -E 's#.*/([^/]+)\.json$#midos-tunnel@\1.service midos-veil@\1.service#'); do
        systemctl disable --now "$relay" 2>/dev/null
    done
    ok "All services stopped"

    stp "2/9" "Removing tunnels & FOU listeners..."
    [[ -x "$DOWN_SCRIPT" ]] && bash "$DOWN_SCRIPT" > /dev/null 2>&1
    local all_tunnels
    all_tunnels=$(ip tunnel show 2>/dev/null | awk -F: '{print $1}' | tr -d ' ' | grep -v '^$')
    if [[ -n "$all_tunnels" ]]; then
        while IFS= read -r tun; do
            [[ -z "$tun" ]] && continue
            is_sys_tunnel "$tun" && continue
            ip link set "$tun" down 2>/dev/null
            ip tunnel del "$tun" 2>/dev/null && ok "Deleted: ${CYAN}${tun}${NC}"
            ip link del "$tun" 2>/dev/null
        done <<< "$all_tunnels"
    fi
    for tun in midosMOI greGRE greKH greIR greIRAN greKHARJ $(seq -f "midosMOI%g" 1 "$MAX_PEERS"); do
        ip link set "$tun" down 2>/dev/null
        ip tunnel del "$tun" 2>/dev/null
        ip link del "$tun" 2>/dev/null
    done
    while read -r p; do
        [[ -n "$p" ]] && ip fou del port "$p" ipproto 47 2>/dev/null
    done < <(ip fou show 2>/dev/null | grep -oP 'port \K[0-9]+')
    ok "User tunnels removed"

    stp "3/9" "Nuking iptables..."
    for chain in "$MIDOS_PRE" "$MIDOS_POST" MIDOS_NAT; do
        for _ in $(seq 1 20); do iptables -t nat -D PREROUTING  -j "$chain" 2>/dev/null || break; done
        for _ in $(seq 1 20); do iptables -t nat -D POSTROUTING -j "$chain" 2>/dev/null || break; done
        iptables -t nat -F "$chain" 2>/dev/null
        iptables -t nat -X "$chain" 2>/dev/null
    done
    for _ in $(seq 1 20); do iptables -D FORWARD -j "$MIDOS_FWD" 2>/dev/null || break; done
    for _ in $(seq 1 20); do iptables -D INPUT   -j "$MIDOS_IN"  2>/dev/null || break; done
    iptables -F "$MIDOS_FWD" 2>/dev/null; iptables -X "$MIDOS_FWD" 2>/dev/null
    iptables -F "$MIDOS_IN"  2>/dev/null; iptables -X "$MIDOS_IN"  2>/dev/null
    for _ in $(seq 1 20); do iptables -t mangle -D FORWARD -j MIDOS_MSS 2>/dev/null || break; done
    iptables -t mangle -F MIDOS_MSS 2>/dev/null; iptables -t mangle -X MIDOS_MSS 2>/dev/null
    ok "Custom chains removed"

    # Only OUR chains are removed. Flushing the whole nat/mangle table would
    # also wipe x-ui, Docker and every other program's rules, so we never do it.
    for ch in VIRA_PRE VIRA_POST; do
        for _ in $(seq 1 20); do ipt -t nat -D PREROUTING  -j "$ch" || break; done
        for _ in $(seq 1 20); do ipt -t nat -D POSTROUTING -j "$ch" || break; done
        ipt -t nat -F "$ch"; ipt -t nat -X "$ch"
    done
    for ch in VIRA_FWD VIRA_IN; do
        for _ in $(seq 1 20); do ipt -D FORWARD -j "$ch" || break; done
        for _ in $(seq 1 20); do ipt -D INPUT   -j "$ch" || break; done
        ipt -F "$ch"; ipt -X "$ch"
    done
    for _ in $(seq 1 20); do ipt -t mangle -D FORWARD -j VIRA_MSS || break; done
    ipt -t mangle -F VIRA_MSS; ipt -t mangle -X VIRA_MSS
    ok "Our chains removed - all other firewall rules left intact"

    # ESP/IPsec left over from any tunnel generation (our policies always carry
    # a "proto gre" selector, which nothing else uses).
    local gp
    gp=$(ip xfrm policy show 2>/dev/null | grep -c 'proto gre')
    if [[ "${gp:-0}" -gt 0 ]]; then
        ip -o xfrm policy show 2>/dev/null | grep 'proto gre' | while read -r l; do
            local s d dir
            s=$(grep -oP 'src \K[0-9./]+' <<< "$l" | head -1)
            d=$(grep -oP 'dst \K[0-9./]+' <<< "$l" | head -1)
            dir=$(grep -oP 'dir \K\w+' <<< "$l" | head -1)
            [[ -n "$s" && -n "$d" && -n "$dir" ]] && ip xfrm policy delete src "$s" dst "$d" proto gre dir "$dir" 2>/dev/null
        done
        ip xfrm state show 2>/dev/null | awk '/^src/{s=$2;d=$4} /sel .*proto gre/{print s,d}' | sort -u |
            while read -r s d; do [[ -n "$s" ]] && ip xfrm state deleteall src "$s" dst "$d" proto esp 2>/dev/null; done
        ok "IPsec state removed (${gp} policies)"
    fi

    stp "4/9" "Persisting the firewall without our rules..."
    # Persist the CURRENT ruleset (ours already removed above). Writing a blank
    # rules.v4 here would delete x-ui / Docker / everything else's saved rules
    # on the next boot, so we never do that - we just re-save what is live now.
    if command -v netfilter-persistent > /dev/null 2>&1; then
        netfilter-persistent save > /dev/null 2>&1 && ok "Live ruleset saved (other software untouched)"
    elif [[ -d /etc/iptables ]]; then
        iptables-save > /etc/iptables/rules.v4 2>/dev/null && ok "Live ruleset saved to rules.v4"
    else
        info "No persistence tool installed - nothing to save"
    fi

    stp "5/9" "Removing services..."
    for svc in Midos-tunnel Midos-tunnel-watchdog midos-tunnel midos-tunnel-watchdog midos-gre greKH greIR; do
        rm -f "/etc/systemd/system/${svc}.service"
    done
    rm -f "$REKEY_SERVICE_FILE" "$REKEY_TIMER_FILE"
    rm -f /etc/systemd/system/midos-tunnel-panel.service /etc/systemd/system/midos-tunnel@.service \
        /etc/systemd/system/midos-veil-panel.service /etc/systemd/system/midos-veil@.service /etc/systemd/system/Midos-tunnel-veil*.service
    systemctl daemon-reload 2>/dev/null
    ok "Service files removed"

    stp "6/9" "Removing scripts..."
    rm -f "$UP_SCRIPT" "$DOWN_SCRIPT" "$WD_SCRIPT" "$NAT_SCRIPT" "$LIC_SCRIPT"
    rm -f /usr/local/sbin/midos-gre.sh /usr/local/sbin/greKH.sh /usr/local/sbin/greIR.sh
    ok "Scripts removed"

    stp "7/9" "Removing config & IPsec..."
    rm -rf "$CONFIG_DIR" /etc/midos-tunnel
    rm -f /etc/systemd/system/midos-tunnel.service /etc/systemd/system/midos-tunnel-watchdog.service
    rm -f /usr/local/sbin/midos-tunnel-up.sh /usr/local/sbin/midos-tunnel-down.sh /usr/local/sbin/midos-tunnel-watchdog.sh /usr/local/sbin/midos-tunnel-nat.sh /usr/local/sbin/midos-tunnel.sh
    rm -f /etc/modules-load.d/gre.conf /etc/modules-load.d/Midos-tunnel.conf /etc/modules-load.d/midos-tunnel.conf /etc/modules-load.d/midos-bbr.conf
    rm -f /etc/sysctl.d/99-midos-tunnel.conf
    if [[ -f /etc/ipsec.conf ]] && grep -q "Midos Tunnel" /etc/ipsec.conf 2>/dev/null; then
        for s in strongswan-starter strongswan; do systemctl stop "$s" 2>/dev/null; systemctl disable "$s" 2>/dev/null; done
        rm -f /etc/ipsec.conf /etc/ipsec.secrets
        ok "IPsec config removed"
    fi
    ok "Config removed"

    stp "8/9" "Reverting kernel tuning..."
    rm -f "$SYSCTL_FILE"
    sed -i '/net.ipv4.ip_forward/d' /etc/sysctl.conf 2>/dev/null
    sysctl -w net.ipv4.ip_forward=0 > /dev/null 2>&1
    sysctl -w net.ipv4.conf.all.rp_filter=1 > /dev/null 2>&1
    sysctl --system > /dev/null 2>&1
    ok "Forwarding OFF, sysctl restored"
    rmmod ip_gre 2>/dev/null && ok "ip_gre unloaded" || info "ip_gre busy (needs reboot)"
    rmmod fou 2>/dev/null && ok "fou unloaded" || true
    rmmod gre 2>/dev/null && ok "gre unloaded" || info "gre busy (needs reboot)"

    stp "9/9" "Verifying..."
    echo ""
    local problems=0 user_tun_count=0 tname
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        tname=$(echo "$line" | awk -F: '{print $1}' | tr -d ' ')
        [[ -z "$tname" ]] && continue
        is_sys_tunnel "$tname" && continue
        user_tun_count=$((user_tun_count + 1))
        err "Tunnel still exists: ${tname}"
    done < <(ip tunnel show 2>/dev/null)
    ((user_tun_count == 0)) && ok "Tunnels: ${GREEN}ALL REMOVED${NC}" || problems=$((problems + user_tun_count))

    local sf=false
    for svc in Midos-tunnel Midos-tunnel-watchdog midos-tunnel midos-tunnel-watchdog midos-gre greKH greIR; do
        [[ -f "/etc/systemd/system/${svc}.service" ]] && { err "Service: ${svc}"; sf=true; problems=$((problems + 1)); }
    done
    $sf || ok "Services: ${GREEN}CLEAN${NC}"

    local scf=false
    for s in "$UP_SCRIPT" "$DOWN_SCRIPT" "$WD_SCRIPT" "$NAT_SCRIPT" "$LIC_SCRIPT" /usr/local/sbin/midos-gre.sh; do
        [[ -f "$s" ]] && { err "Script: $s"; scf=true; problems=$((problems + 1)); }
    done
    $scf || ok "Scripts: ${GREEN}CLEAN${NC}"

    [[ -d "$CONFIG_DIR" ]] && { err "Config exists"; problems=$((problems + 1)); } || ok "Config: ${GREEN}CLEAN${NC}"

    # Only our own chains prove a clean uninstall - rules belonging to x-ui,
    # Docker and friends are supposed to still be there.
    local nr=0 ch
    for ch in MIDOS_PRE MIDOS_POST MIDOS_FWD MIDOS_IN MIDOS_MSS; do
        iptables -t nat -L "$ch" > /dev/null 2>&1 && nr=$((nr + 1))
        iptables -L "$ch" > /dev/null 2>&1 && nr=$((nr + 1))
        iptables -t mangle -L "$ch" > /dev/null 2>&1 && nr=$((nr + 1))
    done
    ((nr > 0)) && { err "${nr} MIDOS chain(s) remain"; problems=$((problems + 1)); } || ok "iptables: ${GREEN}OUR CHAINS GONE${NC} ${DIM}(other software untouched)${NC}"
    [[ "$(cat /proc/sys/net/ipv4/ip_forward 2>/dev/null)" == "0" ]] && ok "Forward: ${GREEN}OFF${NC}" || warn "Forward still ON"

    echo ""
    if ((problems == 0)); then
        echo -e "    ${GREEN}${BOLD}╔══════════════════════════════════════════════════════════╗${NC}"
        echo -e "    ${GREEN}${BOLD}║   ✅ NUCLEAR UNINSTALL COMPLETE!                         ║${NC}"
        echo -e "    ${GREEN}${BOLD}║   🏭 Server back to factory state - SSH intact           ║${NC}"
        echo -e "    ${GREEN}${BOLD}╚══════════════════════════════════════════════════════════╝${NC}"
    else
        echo -e "    ${YELLOW}╔══════════════════════════════════════════════════════════╗${NC}"
        echo -e "    ${YELLOW}║  ⚠  ${problems} item(s) need a reboot to fully clean            ║${NC}"
        echo -e "    ${YELLOW}╚══════════════════════════════════════════════════════════╝${NC}"
    fi

    # Leftovers from an older release (or a different install) are not in this
    # script's tables, so offer the standalone deep uninstaller for those.
    if ((problems > 0)); then
        echo ""
        echo -ne "    ${YELLOW}❯${NC} Run the deep uninstaller to hunt remaining leftovers? [${GREEN}y${NC}/n]: "
        read -r deep
        if [[ "${deep,,}" == "y" || "${deep,,}" == "yes" ]]; then
            local base="$DOWNLOAD_BASE"
            local du="${base%/}/uninstall.sh"
            if curl -fsSL "$du" -o /tmp/.midos-deep-uninstall.sh 2>/dev/null; then
                bash /tmp/.midos-deep-uninstall.sh --yes
                rm -f /tmp/.midos-deep-uninstall.sh
            else
                warn "Could not download ${du}"
            fi
        fi
    fi

    echo ""
    if lsmod | grep -q "ip_gre"; then
        warn "gre0 & ip_gre fully clean after ${WHITE}reboot${NC}"
        echo -ne "    ${RED}❯${NC} Reboot now? [${RED}y${NC}/${GREEN}n${NC}]: "
        read -r rb
        rb=$(echo "$rb" | tr '[:upper:]' '[:lower:]' | xargs)
        if [[ "$rb" == "y" || "$rb" == "yes" ]]; then
            echo ""; ok "Rebooting in 3 seconds..."; sleep 3; reboot
        fi
    else
        ok "Everything clean! No reboot needed."
    fi
    echo ""
    echo -ne "    ${G2}❯${NC} Press Enter..."
    read -r
}

# ──────────────────── MENU ────────────────────

show_main_menu() {
    show_logo
    if [[ -f "$CONFIG_FILE" ]]; then
        load_config 2>/dev/null
        local ri="🌍" ss="${RED}OFF${NC}" up=0 i
        [[ "$ROLE" == "IRAN" ]] && ri="🇮🇷"
        systemctl is-active --quiet Midos-tunnel.service 2>/dev/null && ss="${GREEN}ON${NC}"
        for ((i = 1; i <= PEER_COUNT; i++)); do
            ip link show "$(pget "$i" IFACE)" > /dev/null 2>&1 && up=$((up + 1))
        done
        echo -e "    ${DIM}Server: ${WHITE}${ROLE}${NC} ${ri}  Service: ${ss}  Tunnels: ${CYAN}${up}/${PEER_COUNT}${NC}  Mode: ${CYAN}${TUNNEL_MODE}${NC}  Dist: ${CYAN}${BALANCE_MODE}${NC}"
        if [[ "$ROLE" == "IRAN" ]] && ((PEER_COUNT > 0)); then
            local names=""
            for ((i = 1; i <= PEER_COUNT; i++)); do names="${names}$(pget "$i" NAME) "; done
            echo -e "    ${DIM}KHAREJ: ${CYAN}${names}${NC}"
        fi
        echo ""
    fi
    echo -e "${G3}    ┌──────────────────────────────────────────────────────────────┐${NC}"
    echo -e "${G3}    │${NC}  ${G1}⚙${NC}  ${WHITE}${BOLD}MAIN MENU${NC}                                                ${G3}│${NC}"
    echo -e "${G3}    ├──────────────────────────────────────────────────────────────┤${NC}"
    echo -e "${G3}    │${NC}   ${G2}[1]${NC} ➤ Setup IRAN       ${DIM}(hub - add all KHAREJ servers)${NC}      ${G3}│${NC}"
    echo -e "${G3}    │${NC}   ${G2}[2]${NC} ➤ Setup KHAREJ     ${DIM}(one endpoint, needs its slot)${NC}      ${G3}│${NC}"
    echo -e "${G3}    │${NC}   ${G2}[3]${NC} ➤ Status & Ping    ${DIM}(all peers, full diagnostics)${NC}       ${G3}│${NC}"
    echo -e "${G3}    │${NC}   ${G2}[4]${NC} ➤ Restart Tunnels  ${DIM}(rebuild + test)${NC}                    ${G3}│${NC}"
    echo -e "${G3}    │${NC}   ${G2}[5]${NC} ➤ Advanced         ${DIM}(mode / MTU / watchdog / shaping)${NC}   ${G3}│${NC}"
    echo -e "${G3}    │${NC}   ${G2}[6]${NC} ➤ ${WHITE}Servers${NC}          ${DIM}(add / remove / balance / failover)${NC} ${G3}│${NC}"
    echo -e "${G3}    │${NC}   ${G2}[7]${NC} ➤ Live Monitor     ${DIM}(per-tunnel bandwidth + RTT)${NC}        ${G3}│${NC}"
    echo -e "${G3}    │${NC}   ${G2}[8]${NC} ➤ ${WHITE}Backup Path${NC}      ${DIM}(camouflaged relay, when blocked)${NC}   ${G3}│${NC}"
    echo -e "${G3}    │${NC}   ${G2}[10]${NC} ➤ ${WHITE}Web Panel${NC}       ${DIM}(browser dashboard & management)${NC}    ${G3}│${NC}"
    echo -e "${G3}    │${NC}   ${G2}[9]${NC} ➤ ${RED}Uninstall${NC}        ${DIM}(nuclear complete reset)${NC}            ${G3}│${NC}"
    echo -e "${G3}    │${NC}   ${RED}[0]${NC} ➤ Exit                                                   ${G3}│${NC}"
    echo -e "${G3}    └──────────────────────────────────────────────────────────────┘${NC}"
    echo ""
    echo -ne "    ${G2}❯${NC} Choice: "
}

main() {
    # Non-interactive entrypoints for the web panel and automation. Anything
    # else falls through to the interactive menu exactly as before.
    case "${1:-}" in
        provision) shift; provision "$@"; exit $? ;;
        teardown) shift; teardown "$@"; exit $? ;;
        prepare-kernel) shift; check_root; prepare_kernel_base; exit $? ;;
    esac
    check_root
    touch "$LOG_FILE" 2>/dev/null
    while true; do
        show_main_menu
        read -r ch
        case $ch in
            1) setup_iran ;;
            2) setup_kharej ;;
            3) check_status ;;
            4) restart_tunnel ;;
            5) advanced_settings ;;
            6) manage_servers ;;
            7) live_monitor ;;
            8) veil_menu ;;
            9) uninstall_tunnel ;;
            10) panel_menu ;;
            0)
                echo ""
                echo -e "    ${G2}★${NC} Thank you for using ${G1}Midos Tunnel${NC}!"
                echo ""
                exit 0
                ;;
            *) err "Invalid!"; sleep 1 ;;
        esac
    done
}

main "$@"
