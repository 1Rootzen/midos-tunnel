<script setup>
import { ref, shallowRef } from 'vue';
import { api } from '../lib/api.js';
import { usePoll } from '../lib/poll.js';
import { store } from '../lib/store.js';
import { fleetTunnels } from '../lib/fleet.js';
import PageHead from '../components/PageHead.vue';
import TunnelRow from '../components/TunnelRow.vue';
import KernelRow from '../components/KernelRow.vue';
import FleetTunnelRow from '../components/FleetTunnelRow.vue';
import AppIcon from '../components/AppIcon.vue';

const tunnels = shallowRef([]);
const kernel = shallowRef(null);
const cables = shallowRef([]);
const loaded = ref(false);
usePoll(async () => {
  const [t, k, f] = await Promise.all([api('GET', '/api/tunnels'), api('GET', '/api/kernel').catch(() => null), api('GET', '/api/fleet').catch(() => null)]);
  tunnels.value = t;
  cables.value = fleetTunnels(f);
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
      <div v-if="loaded && !tunnels.length && !kernel && !cables.length" class="card empty">{{ $t('هنوز تانلی ساخته نشده است.') }}</div>
    </div>
    <template v-if="cables.length">
      <div class="section-title">{{ $t('تانل‌های بین سرورهای دیگر') }}</div>
      <div class="tunnels"><FleetTunnelRow v-for="c in cables" :key="c.id" :c="c" /></div>
    </template>
  </div>
</template>
