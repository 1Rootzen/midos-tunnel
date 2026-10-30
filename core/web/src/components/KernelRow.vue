<script setup>
import { computed } from 'vue';
import { fa } from '../lib/format.js';
import { STATE_FA } from '../lib/constants.js';
import AppIcon from './AppIcon.vue';

// The server's kernel tunnel (ESP / MOI) as one row in the tunnel list; it
// opens the kernel tunnel's own page for start/stop, peers and logs.
const props = defineProps({ k: { type: Object, required: true } });
const MODE = { esp: 'ESP', espudp: 'ESP-in-UDP', ipsec: 'IPsec', gre: 'MOI', fou: 'FOU' };
const main = computed(() => (props.k.units['Midos-tunnel.service'] || {}).active || 'unknown');
const up = computed(() => props.k.peers.filter((p) => p.health === 'up').length);
const dot = computed(() => (main.value === 'failed' ? 'bad' : main.value !== 'active' ? '' : up.value === props.k.peers.filter((p) => p.enabled).length ? 'ok' : 'warn'));
</script>

<template>
  <router-link to="/kernel" class="trow card krow">
    <div class="tname"><i class="dot" :class="dot"></i><span>{{ $t('تانل کرنلی') }}</span><span class="pill accent">KERNEL</span></div>
    <div class="tcell"><div class="tag">ROLE</div><div class="v">{{ $t(k.role === 'IRAN' ? 'ایران · هاب' : 'خارج') }}</div></div>
    <div class="tcell"><div class="tag">LINK</div><div class="v">{{ MODE[k.mode] || k.mode }}</div></div>
    <div class="tcell"><div class="tag">PEERS</div><div class="v num">{{ $t('{up} / {total} سالم', { up: fa(up), total: fa(k.peers.length) }) }}</div></div>
    <div class="tcell"><div class="tag">STATE</div><div class="v">{{ $t(STATE_FA[main] || main) }}</div></div>
    <AppIcon name="chev" class="chev" />
  </router-link>
</template>
