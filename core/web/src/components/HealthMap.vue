<script setup>
import { computed, ref, onMounted, onBeforeUnmount } from 'vue';
import { summary, health } from '../lib/summary.js';

// The mother server on the left and every tunnel in a column to its right, each
// joined to it by a cable coloured by live health and labelled with its RTT.
// The kernel tunnel of this server and the tunnels the designer built between
// two other servers (labelled with both ends) are in the column too.
const props = defineProps({
  tunnels: { type: Array, default: () => [] },
  kernel: { type: Object, default: null },
  cables: { type: Array, default: () => [] },
});
const COLOR = { ok: '#3ee0a1', warn: '#ffc24d', bad: '#ff4d5e', off: '#6b7581' };
const CABLE = { up: 'ok', warn: 'warn', down: 'bad', missing: 'bad', off: 'off', draft: 'off' };
const ROW = 46;

// drawn at the card's real width, so the text keeps its size on any screen;
// a phone gets the mother close to the edge to leave the labels room
const svg = ref(null);
const W = ref(640);
let ro = null;
onMounted(() => {
  const fit = () => { if (svg.value) W.value = Math.max(300, Math.round(svg.value.clientWidth)); };
  fit();
  if (window.ResizeObserver) { ro = new ResizeObserver(fit); ro.observe(svg.value); }
});
onBeforeUnmount(() => { if (ro) ro.disconnect(); });
const MX = computed(() => (W.value < 600 ? 44 : Math.round(W.value * 0.26)));
const NX = computed(() => (W.value < 600 ? Math.round(W.value * 0.36) : Math.round(W.value * 0.5)));

function kernelHealth(k) {
  const main = (k.units['Midos-tunnel.service'] || {}).active;
  if (main === 'failed') return 'bad';
  if (main !== 'active') return 'off';
  const up = k.peers.filter((p) => p.health === 'up').length;
  return up === k.peers.filter((p) => p.enabled).length ? 'ok' : 'warn';
}

const items = computed(() => {
  const out = props.tunnels.map((t) => {
    const sm = summary(t);
    return { id: t.id, label: t.id, h: health(t), rtt: sm.carriers && sm.rtt != null ? sm.rtt : null };
  });
  if (props.kernel) out.push({ id: 'kernel', label: 'kernel', h: kernelHealth(props.kernel), rtt: null });
  for (const c of props.cables) out.push({ id: 'c-' + c.id, label: c.from + ' ⇄ ' + c.to, h: CABLE[c.st] || 'off', rtt: c.rtt });
  return out;
});

const H = computed(() => Math.max(200, items.value.length * ROW + 56));
const my = computed(() => H.value / 2);

const nodes = computed(() => {
  const top = (H.value - items.value.length * ROW) / 2 + ROW / 2;
  return items.value.map((it, i) => {
    const y = top + i * ROW;
    const m = my.value, mx = MX.value, nx = NX.value, c = (nx - mx) * 0.45;
    return { id: it.id, label: it.label, y, color: COLOR[it.h], off: it.h === 'off',
      flow: it.h === 'ok' || it.h === 'warn', rtt: it.rtt != null ? Math.round(it.rtt) + 'ms' : '',
      path: `M ${mx + 12} ${m} C ${mx + c} ${m}, ${nx - c} ${y}, ${nx - 8} ${y}` };
  });
});
</script>

<template>
  <svg ref="svg" class="map" :style="{ height: H + 'px' }" :viewBox="`0 0 ${W} ${H}`" direction="ltr" role="img" :aria-label="$t('نقشهٔ سلامت تانل‌ها')">
    <defs>
      <radialGradient id="hmCore"><stop offset="0" stop-color="#8f96f0" /><stop offset="1" stop-color="#3d45aa" /></radialGradient>
    </defs>
    <g v-for="n in nodes" :key="'l' + n.id">
      <path :d="n.path" :stroke="n.color" class="map-link" :class="{ off: n.off }" />
      <path v-if="n.flow" :d="n.path" class="map-flow" />
    </g>
    <g v-for="n in nodes" :key="'n' + n.id" class="map-node">
      <circle :cx="NX" :cy="n.y" r="11" :fill="n.color" fill-opacity="0.16" />
      <circle :cx="NX" :cy="n.y" r="6" :fill="n.color" class="map-dot" :style="{ color: n.color }" />
      <text :x="NX + 18" :y="n.rtt ? n.y - 1 : n.y + 4" class="map-label">{{ n.label }}</text>
      <text v-if="n.rtt" :x="NX + 18" :y="n.y + 14" class="map-rtt">{{ n.rtt }}</text>
    </g>
    <circle :cx="MX" :cy="my" r="22" fill="#3d45aa" fill-opacity="0.12" class="map-halo" />
    <circle :cx="MX" :cy="my" r="12" fill="url(#hmCore)" class="map-core" />
    <text :x="MX" :y="my + 38" text-anchor="middle" class="map-label on">{{ $t('این سرور') }}</text>
    <text v-if="!nodes.length" :x="NX" :y="my + 4" class="map-rtt">{{ $t('هنوز تانلی نیست') }}</text>
  </svg>
</template>

<style>
svg.map { width: 100%; display: block; overflow: visible; }
.map-link { fill: none; stroke-width: 2; stroke-opacity: 0.55; }
.map-link.off { stroke-dasharray: 4 5; stroke-opacity: 0.35; }
.map-flow { fill: none; stroke: #fff; stroke-width: 2.4; stroke-linecap: round; stroke-dasharray: 1 22; stroke-opacity: 0.85; animation: mapFlow 1.4s linear infinite; }
@keyframes mapFlow { to { stroke-dashoffset: -23; } }
.map-dot { filter: drop-shadow(0 0 5px currentColor); }
.map-node { transition: transform 0.3s; }
.map-label { fill: var(--ti-2); font: 600 13px var(--font); }
.map-label.on { fill: #fff; }
.map-rtt { fill: var(--ti-3); font: 500 11.5px var(--font); }
.map-core { filter: drop-shadow(0 0 8px #3d45aa); }
.map-halo { animation: halo 2.6s ease-in-out infinite; transform-box: fill-box; transform-origin: center; }
@keyframes halo { 50% { transform: scale(1.5); opacity: 0.4; } }
</style>
