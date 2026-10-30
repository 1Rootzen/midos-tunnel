<script setup>
import { ref, computed, shallowRef } from 'vue';
import { api } from '../lib/api.js';
import { usePoll } from '../lib/poll.js';
import { store } from '../lib/store.js';
import { fa, bytes, mbit, dur } from '../lib/format.js';
import { pct } from '../lib/i18n.js';
import PageHead from '../components/PageHead.vue';
import RingGauge from '../components/RingGauge.vue';
import LineChart from '../components/LineChart.vue';
import HealthMap from '../components/HealthMap.vue';
import TunnelRow from '../components/TunnelRow.vue';
import KernelRow from '../components/KernelRow.vue';
import AppIcon from '../components/AppIcon.vue';

const sys = shallowRef(null);
const tunnels = shallowRef([]);
const kernel = shallowRef(null);
const loaded = ref(false);

usePoll(async () => {
  const [s, t, k] = await Promise.all([api('GET', '/api/system'), api('GET', '/api/tunnels'), api('GET', '/api/kernel').catch(() => null)]);
  sys.value = s;
  tunnels.value = t;
  kernel.value = k && k.installed ? k : null;
  loaded.value = true;
}, 3000);

const last = computed(() => (sys.value && sys.value.history[sys.value.history.length - 1]) || { cpu: 0, rx: 0, tx: 0 });
const memPct = computed(() => (sys.value && sys.value.mem_total ? 100 * sys.value.mem_used / sys.value.mem_total : 0));
const diskPct = computed(() => (sys.value && sys.value.disk_total ? 100 * sys.value.disk_used / sys.value.disk_total : 0));
const NET = [{ key: 'rx', color: '#6c75e6', glow: true }, { key: 'tx', color: '#c7cdd3' }];
const CPU = [{ key: 'cpu', color: '#6c75e6', glow: true }];
</script>

<template>
  <div>
    <PageHead :title="$t('داشبورد')" tag="SYSTEM STATUS // LIVE">
      <button class="btn primary" @click="store.newTunnel = true"><AppIcon name="plus" /> {{ $t('تانل جدید') }}</button>
    </PageHead>

    <div class="grid">
      <RingGauge label="CPU" :pct="last.cpu" :value="sys ? fa(Math.round(last.cpu)) + pct() : '—'" :sub="sys ? $t('{n} هسته', { n: fa(sys.cpus) }) : ''" />
      <RingGauge label="MEMORY" :pct="memPct" :value="sys ? fa(Math.round(memPct)) + pct() : '—'" :sub="sys ? $t('{used} از {total}', { used: bytes(sys.mem_used), total: bytes(sys.mem_total) }) : ''" />
      <RingGauge label="DISK" :pct="diskPct" :value="sys ? fa(Math.round(diskPct)) + pct() : '—'" :sub="sys ? $t('{used} از {total}', { used: bytes(sys.disk_used), total: bytes(sys.disk_total) }) : ''" />
      <div class="card">
        <div class="tag">UPTIME / LOAD</div>
        <div class="stat-value text">{{ sys ? dur(sys.uptime_s) : '—' }}</div>
        <div class="stat-sub ltr">load {{ sys ? sys.load : '—' }}</div>
      </div>
    </div>

    <div class="grid-2">
      <div class="card">
        <div class="card-head"><h3><AppIcon name="net" /> {{ $t('ترافیک شبکه') }}</h3><span class="tag">{{ sys ? sys.iface : '' }}</span></div>
        <LineChart :points="sys ? sys.history : []" :series="NET" />
        <div class="legend"><span><i class="rx"></i>{{ $t('دریافت') }} <b class="num">{{ mbit(last.rx) }}</b></span><span><i class="tx"></i>{{ $t('ارسال') }} <b class="num">{{ mbit(last.tx) }}</b></span></div>
      </div>
      <div class="card">
        <div class="card-head"><h3><AppIcon name="cpu" /> {{ $t('بار پردازنده') }}</h3></div>
        <LineChart :points="sys ? sys.history : []" :series="CPU" />
        <div class="legend"><span><i class="rx"></i>{{ $t('اکنون') }} <b class="num">{{ fa(Math.round(last.cpu)) }}{{ pct() }}</b></span></div>
      </div>
    </div>

    <div class="card map-card">
      <div class="card-head"><h3><AppIcon name="node" /> {{ $t('نقشهٔ سلامت') }}</h3><span class="tag">LIVE TOPOLOGY</span></div>
      <HealthMap :tunnels="tunnels" />
    </div>

    <div class="section-title">{{ $t('تانل‌ها') }}</div>
    <div class="tunnels">
      <KernelRow v-if="kernel" :k="kernel" />
      <TunnelRow v-for="t in tunnels" :key="t.id" :t="t" />
      <div v-if="loaded && !tunnels.length && !kernel" class="card empty">
        {{ $t('هنوز تانلی نیست.') }} <a href="#" @click.prevent="store.newTunnel = true">{{ $t('یک تانل بسازید') }}</a>
      </div>
    </div>
  </div>
</template>

<style>
.map-card { margin-top: 16px; }
</style>
