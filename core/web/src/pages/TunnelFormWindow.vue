<script setup>
import { useRouter } from 'vue-router';
import GlassWindow from '../components/GlassWindow.vue';
import TunnelForm from '../components/TunnelForm.vue';
import { store } from '../lib/store.js';

const emit = defineEmits(['close']);
const router = useRouter();
function done(name, kind) {
  emit('close');
  router.push(kind === 'kernel' ? '/kernel' : '/tunnel/' + encodeURIComponent(name));
}
</script>

<template>
  <GlassWindow :title="$t('تانل جدید')" icon="plus" :width="1000" @close="emit('close')">
    <TunnelForm :kind="store.newTunnelKind" @done="done" @cancel="emit('close')" />
  </GlassWindow>
</template>
