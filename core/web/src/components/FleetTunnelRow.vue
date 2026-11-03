<script setup>
import { computed } from 'vue';
import { STATE_TEXT } from '../lib/fleet.js';
import { ms } from '../lib/format.js';
import { fwd } from '../lib/i18n.js';
import AppIcon from './AppIcon.vue';

// A tunnel the network designer built between two other servers. It opens the
// designer, where the cable is managed; its edit button changes the cable here.
const props = defineProps({ c: { type: Object, required: true } });
const emit = defineEmits(['edit']);
const DOT = { up: 'ok', warn: 'warn', down: 'bad', missing: 'bad', off: '', draft: '' };
const dot = computed(() => DOT[props.c.st] || '');
</script>

<template>
  <router-link to="/topology" class="trow card">
    <div class="tname"><i class="dot" :class="dot"></i><span>{{ c.name }}</span><span class="pill">{{ $t('شبکه') }}</span></div>
    <div class="tcell"><div class="tag">SERVERS</div><div class="v">{{ c.from }} {{ fwd() }} {{ c.to }}</div>
      <div v-if="c.ports.length || c.udp.length" class="tports" dir="ltr" :title="$t('پورت روی ایران → پورت روی سرور خارج')">
        <span v-for="(p, k) in c.ports" :key="'t' + k">{{ p.iran === p.remote ? p.iran : p.iran + '→' + p.remote }}</span>
        <span v-for="(p, k) in c.udp" :key="'u' + k">UDP {{ p.iran === p.remote ? p.iran : p.iran + '→' + p.remote }}</span>
      </div></div>
    <div class="tcell"><div class="tag">LINK</div><div class="v">{{ c.kind }}</div></div>
    <div class="tcell"><div class="tag">RTT</div><div class="v"><span v-if="c.rtt" class="num">{{ ms(c.rtt) }}</span><span v-else class="faint">—</span></div></div>
    <div class="tcell"><div class="tag">STATUS</div><div class="v">{{ $t(STATE_TEXT[c.st] || c.st) }}</div></div>
    <span class="trow-end">
      <button type="button" class="btn small icon-only trow-edit" :title="$t('ویرایش')" @click.prevent.stop="emit('edit', c.id)"><AppIcon name="edit" /></button>
      <AppIcon name="chev" class="chev" />
    </span>
  </router-link>
</template>
