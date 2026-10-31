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
import CableEditWindow from '../components/CableEditWindow.vue';
import AppIcon from '../components/AppIcon.vue';

const tunnels = shallowRef([]);
const kernel = shallowRef(null);
const cables = shallowRef([]);
// tunnels on this server that the designer built: tunnel name -> cable id
// ("kernel" for the kernel tunnel), so their edit changes both servers
const ownCables = shallowRef({});
const loaded = ref(false);
const editing = ref('');
const poll = usePoll(async () => {
  const [t, k, f] = await Promise.all([api('GET', '/api/tunnels'), api('GET', '/api/kernel').catch(() => null), api('GET', '/api/fleet').catch(() => null)]);
  tunnels.value = t;
  cables.value = fleetTunnels(f);
  const own = {};
  for (const l of (f && f.links) || []) if (l.tunnel && (l.a === 'self' || l.b === 'self')) own[l.tunnel] = own[l.tunnel] || l.id;
  ownCables.value = own;
  kernel.value = k && k.installed ? k : null;
  loaded.value = true;
}, 3000);
const edited = () => poll.refresh();
</script>

<template>
  <div>
    <PageHead :title="$t('تانل‌ها')" tag="RELAYS">
      <button class="btn primary" @click="store.newTunnel = true"><AppIcon name="plus" /> {{ $t('تانل جدید') }}</button>
    </PageHead>
    <div class="tunnels">
      <KernelRow v-if="kernel" :k="kernel" :cable="ownCables.kernel || ''" @edit-cable="editing = $event" />
      <TunnelRow v-for="t in tunnels" :key="t.id" :t="t" :cable="ownCables[t.id] || ''" @edit-cable="editing = $event" />
      <div v-if="loaded && !tunnels.length && !kernel && !cables.length" class="card empty">{{ $t('هنوز تانلی ساخته نشده است.') }}</div>
    </div>
    <template v-if="cables.length">
      <div class="section-title">{{ $t('تانل‌های بین سرورهای دیگر') }}</div>
      <div class="tunnels"><FleetTunnelRow v-for="c in cables" :key="c.id" :c="c" @edit="editing = $event" /></div>
    </template>
    <CableEditWindow v-if="editing" :id="editing" @close="editing = ''" @saved="edited" />
  </div>
</template>
