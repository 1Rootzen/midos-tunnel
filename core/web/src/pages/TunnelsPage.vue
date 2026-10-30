<script setup>
import { ref, shallowRef } from 'vue';
import { api } from '../lib/api.js';
import { usePoll } from '../lib/poll.js';
import { store } from '../lib/store.js';
import PageHead from '../components/PageHead.vue';
import TunnelRow from '../components/TunnelRow.vue';
import KernelRow from '../components/KernelRow.vue';
import AppIcon from '../components/AppIcon.vue';

const tunnels = shallowRef([]);
const kernel = shallowRef(null);
const loaded = ref(false);
usePoll(async () => {
  const [t, k] = await Promise.all([api('GET', '/api/tunnels'), api('GET', '/api/kernel').catch(() => null)]);
  tunnels.value = t;
  kernel.value = k && k.installed ? k : null;
  loaded.value = true;
}, 3000);
</script>

<template>
  <div>
    <PageHead :title="$t('تانل‌ها')" tag="RELAYS">
      <button class="btn primary" @click="store.newTunnel = true"><AppIcon name="plus" /> {{ $t('تانل جدید') }}</button>
    </PageHead>
    <div class="tunnels">
      <KernelRow v-if="kernel" :k="kernel" />
      <TunnelRow v-for="t in tunnels" :key="t.id" :t="t" />
      <div v-if="loaded && !tunnels.length && !kernel" class="card empty">{{ $t('هنوز تانلی ساخته نشده است.') }}</div>
    </div>
  </div>
</template>
