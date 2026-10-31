<script setup>
import { computed } from 'vue';
import { fa } from '../lib/format.js';
import { STATE_FA } from '../lib/constants.js';
import AppIcon from './AppIcon.vue';

// The server's kernel tunnel (ESP / MOI) as one row in the tunnel list; it
// opens the kernel tunnel's own page for start/stop, peers and logs.
// cable: set when the network designer built it - it can then be edited
const props = defineProps({ k: { type: Object, required: true }, cable: { type: String, default: '' } });
const emit = defineEmits(['edit-cable']);
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
    <span class="trow-end">
      <button v-if="cable" type="button" class="btn small icon-only trow-edit" :title="$t('ویرایش')" @click.prevent.stop="emit('edit-cable', cable)"><AppIcon name="edit" /></button>
      <AppIcon name="chev" class="chev" />
    </span>
  </router-link>
</template>
