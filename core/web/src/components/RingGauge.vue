<script setup>
import { computed } from 'vue';

const props = defineProps({
  label: String,
  pct: { type: Number, default: 0 },
  value: { type: String, default: '—' },
  sub: { type: String, default: '' },
});
const R = 32;
const C = 2 * Math.PI * R;
const offset = computed(() => C * (1 - Math.max(0, Math.min(100, props.pct || 0)) / 100));
const gid = 'g' + Math.random().toString(36).slice(2, 8);
</script>

<template>
  <div class="card gauge" :class="{ hot: pct >= 90 }">
    <svg viewBox="0 0 78 78">
      <defs>
        <linearGradient :id="gid" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stop-color="#8f96f0" />
          <stop offset="1" stop-color="#3d45aa" />
        </linearGradient>
      </defs>
      <circle cx="39" cy="39" :r="R" fill="none" class="ticks" stroke-width="8" :stroke-dasharray="'1 ' + (C / 40 - 1)" />
      <circle cx="39" cy="39" :r="R" fill="none" class="track" stroke-width="3" />
      <circle cx="39" cy="39" :r="R" fill="none" class="meter" stroke-width="4" :stroke="pct >= 90 ? '#ff4d5e' : `url(#${gid})`"
        :stroke-dasharray="C" :stroke-dashoffset="offset" />
    </svg>
    <div>
      <div class="tag">{{ label }}</div>
      <div class="stat-value">{{ value }}</div>
      <div class="stat-sub">{{ sub }}</div>
    </div>
  </div>
</template>
