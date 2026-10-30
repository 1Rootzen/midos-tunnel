<script setup>
import { computed } from 'vue';
import { summary, health } from '../lib/summary.js';

// A radial topology: this server at the centre, each tunnel a node around it,
// the link coloured by live health and labelled with its RTT.
const props = defineProps({ tunnels: { type: Array, default: () => [] } });
const COLOR = { ok: '#3ee0a1', warn: '#ffc24d', bad: '#ff4d5e', off: '#6b7581' };
const cx = 300, cy = 122, rx = 232, ry = 86;

const nodes = computed(() => {
  const n = props.tunnels.length;
  return props.tunnels.map((t, i) => {
    const a = (2 * Math.PI * i) / Math.max(1, n) - Math.PI / 2;
    const x = cx + rx * Math.cos(a), y = cy + ry * Math.sin(a);
    const sm = summary(t);
    const h = health(t);
    return { id: t.id, x, y, below: y > cy + 4, color: COLOR[h], off: h === 'off',
      flow: h === 'ok' || h === 'warn', rtt: sm.carriers && sm.rtt != null ? Math.round(sm.rtt) + 'ms' : '' };
  });
});
</script>

<template>
  <svg class="map" viewBox="0 0 600 248" role="img" :aria-label="$t('نقشهٔ سلامت تانل‌ها')">
    <defs>
      <radialGradient id="hmCore"><stop offset="0" stop-color="#8f96f0" /><stop offset="1" stop-color="#3d45aa" /></radialGradient>
    </defs>
    <ellipse :cx="cx" :cy="cy" :rx="rx" :ry="ry" class="map-orbit" />
    <g v-for="n in nodes" :key="'l' + n.id">
      <line :x1="cx" :y1="cy" :x2="n.x" :y2="n.y" :stroke="n.color" class="map-link" :class="{ off: n.off }" />
      <line v-if="n.flow" :x1="cx" :y1="cy" :x2="n.x" :y2="n.y" class="map-flow" />
    </g>
    <g v-for="n in nodes" :key="'n' + n.id" class="map-node">
      <circle :cx="n.x" :cy="n.y" r="11" :fill="n.color" fill-opacity="0.16" />
      <circle :cx="n.x" :cy="n.y" r="6" :fill="n.color" class="map-dot" :style="{ color: n.color }" />
      <text :x="n.x" :y="n.below ? n.y + 22 : n.y - 15" text-anchor="middle" class="map-label">{{ n.id }}</text>
      <text v-if="n.rtt" :x="n.x" :y="n.below ? n.y + 35 : n.y - 28" text-anchor="middle" class="map-rtt">{{ n.rtt }}</text>
    </g>
    <circle :cx="cx" :cy="cy" r="20" fill="#3d45aa" fill-opacity="0.12" class="map-halo" />
    <circle :cx="cx" :cy="cy" r="10" fill="url(#hmCore)" class="map-core" />
    <text :x="cx" :y="cy + 34" text-anchor="middle" class="map-label on">{{ $t('این سرور') }}</text>
    <text v-if="!nodes.length" :x="cx" :y="cy - 34" text-anchor="middle" class="map-rtt">{{ $t('هنوز تانلی نیست') }}</text>
  </svg>
</template>

<style>
svg.map { width: 100%; height: auto; max-height: 300px; display: block; overflow: visible; }
.map-orbit { fill: none; stroke: rgba(255, 255, 255, 0.05); stroke-dasharray: 3 7; }
.map-link { stroke-width: 2; stroke-opacity: 0.55; }
.map-link.off { stroke-dasharray: 4 5; stroke-opacity: 0.35; }
.map-flow { stroke: #fff; stroke-width: 2.4; stroke-linecap: round; stroke-dasharray: 1 22; stroke-opacity: 0.85; animation: mapFlow 1.4s linear infinite; }
@keyframes mapFlow { to { stroke-dashoffset: -23; } }
.map-dot { filter: drop-shadow(0 0 5px currentColor); }
.map-node { transition: transform 0.3s; }
.map-label { fill: var(--ti-2); font: 600 11.5px var(--font); }
.map-label.on { fill: #fff; }
.map-rtt { fill: var(--ti-3); font: 500 10.5px var(--font); direction: ltr; }
.map-core { filter: drop-shadow(0 0 8px #3d45aa); }
.map-halo { animation: halo 2.6s ease-in-out infinite; transform-box: fill-box; transform-origin: center; }
@keyframes halo { 50% { transform: scale(1.5); opacity: 0.4; } }
</style>
