<script setup>
import { computed } from 'vue';
import { summary, roleText, health } from '../lib/summary.js';
import { bytes, ms } from '../lib/format.js';
import AppIcon from './AppIcon.vue';

const props = defineProps({ t: { type: Object, required: true } });
const sm = computed(() => summary(props.t));
const dot = computed(() => ({ ok: 'ok', warn: 'warn', bad: 'bad', off: '' })[health(props.t)]);
</script>

<template>
  <router-link :to="'/tunnel/' + encodeURIComponent(t.id)" class="trow card">
    <div class="tname"><i class="dot" :class="dot"></i><span>{{ t.id }}</span><span v-if="!t.managed" class="pill">{{ $t('قدیمی') }}</span></div>
    <div class="tcell"><div class="tag">ROLE</div><div class="v">{{ roleText(sm.c) }}</div></div>
    <div class="tcell"><div class="tag">LINK</div><div class="v">{{ sm.transport }}</div></div>
    <div class="tcell"><div class="tag">RTT</div>
      <div class="v">
        <span v-if="sm.carriers === null" class="faint">{{ $t('بدون آمار') }}</span>
        <span v-else-if="!sm.carriers" class="faint">{{ $t('بدون حامل') }}</span>
        <span v-else class="num">{{ ms(sm.rtt) }}</span>
      </div>
    </div>
    <div class="tcell"><div class="tag">TRAFFIC</div><div class="v num">{{ sm.s ? bytes(sm.up + sm.down) : '—' }}</div></div>
    <AppIcon name="chev" class="chev" />
  </router-link>
</template>

<style>
a.trow { color: inherit; }
</style>
