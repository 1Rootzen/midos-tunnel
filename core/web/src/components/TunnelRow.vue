<script setup>
import { computed } from 'vue';
import { useRouter } from 'vue-router';
import { summary, roleText, health } from '../lib/summary.js';
import { bytes, ms } from '../lib/format.js';
import AppIcon from './AppIcon.vue';

// cable: set when the network designer built this tunnel - it is then edited
// as a cable, so both of its servers change together
const props = defineProps({ t: { type: Object, required: true }, cable: { type: String, default: '' } });
const emit = defineEmits(['edit-cable']);
const router = useRouter();
function edit() {
  if (props.cable) emit('edit-cable', props.cable);
  else router.push('/edit/' + encodeURIComponent(props.t.id));
}
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
    <span class="trow-end">
      <button type="button" class="btn small icon-only trow-edit" :title="$t('ویرایش')" @click.prevent.stop="edit"><AppIcon name="edit" /></button>
      <AppIcon name="chev" class="chev" />
    </span>
  </router-link>
</template>

<style>
a.trow { color: inherit; }
</style>
