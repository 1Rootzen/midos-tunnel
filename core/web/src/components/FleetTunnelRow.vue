<script setup>
import { computed } from 'vue';
import { STATE_TEXT } from '../lib/fleet.js';
import { ms } from '../lib/format.js';
import { fwd } from '../lib/i18n.js';
import AppIcon from './AppIcon.vue';

// A tunnel the network designer built between two other servers. It opens the
// designer, where the cable is managed.
const props = defineProps({ c: { type: Object, required: true } });
const DOT = { up: 'ok', warn: 'warn', down: 'bad', missing: 'bad', off: '', draft: '' };
const dot = computed(() => DOT[props.c.st] || '');
</script>

<template>
  <router-link to="/topology" class="trow card">
    <div class="tname"><i class="dot" :class="dot"></i><span>{{ c.name }}</span><span class="pill">{{ $t('شبکه') }}</span></div>
    <div class="tcell"><div class="tag">SERVERS</div><div class="v">{{ c.from }} {{ fwd() }} {{ c.to }}</div></div>
    <div class="tcell"><div class="tag">LINK</div><div class="v">{{ c.kind }}</div></div>
    <div class="tcell"><div class="tag">RTT</div><div class="v"><span v-if="c.rtt" class="num">{{ ms(c.rtt) }}</span><span v-else class="faint">—</span></div></div>
    <div class="tcell"><div class="tag">STATUS</div><div class="v">{{ $t(STATE_TEXT[c.st] || c.st) }}</div></div>
    <AppIcon name="chev" class="chev" />
  </router-link>
</template>
