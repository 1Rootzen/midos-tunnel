<script setup>
import { ref, shallowRef, nextTick } from 'vue';
import { api } from '../lib/api.js';
import { store } from '../lib/store.js';
import { usePoll } from '../lib/poll.js';
import { fail, toast } from '../lib/toast.js';
import { confirmBox } from '../lib/confirm.js';
import { fa, ms } from '../lib/format.js';
import { t, pct } from '../lib/i18n.js';
import { STATE_FA, stateClass } from '../lib/constants.js';
import PageHead from '../components/PageHead.vue';
import AppIcon from '../components/AppIcon.vue';

// The kernel tunnel (MOI/GRE + ESP engine): its full state, start/stop/restart,
// build it on both ends, or tear it down.
const HEALTH = { up: ['ok', 'سالم'], down: ['bad', 'قطع'], disabled: ['', 'غیرفعال'], unknown: ['warn', 'نامشخص'] };
const UNIT_FA = {
  'Midos-tunnel.service': 'تانل اصلی', 'Midos-tunnel-watchdog.service': 'دیده‌بان',
  'Midos-tunnel-rekey.timer': 'تعویض کلید', 'Midos-tunnel-encap.service': 'ESP-in-UDP',
};
const MODE_FA = { esp: 'ESP (رمزدار)', espudp: 'ESP-in-UDP', ipsec: 'IPsec', gre: 'MOI', fou: 'FOU' };

const d = shallowRef(null);
const busy = ref('');
const logs = ref(null);
const logsBox = ref(null);
const ping = ref({});
const pinging = ref(false);

const { refresh } = usePoll(async () => { d.value = await api('GET', '/api/kernel'); }, 4000);

const main = () => ((d.value && d.value.units['Midos-tunnel.service']) || {}).active;

async function act(action, label) {
  if (action === 'stop' && !await confirmBox(t('توقف تانل کرنلی'), t('همهٔ همتاها قطع می‌شوند و کاربران روی این مسیر وصل نمی‌مانند.'), t('توقف'), true)) return;
  busy.value = action;
  try { d.value = await api('POST', '/api/kernel/action', { action }); toast(label, 'ok'); }
  catch (e) { fail(e); }
  busy.value = '';
}

async function teardown() {
  if (!await confirmBox(t('حذف تانل کرنلی'), t('تانل کرنلی این سرور حذف می‌شود. تانل‌های مستقیم و معکوس و پنل دست‌نخورده می‌مانند.'), t('حذف'), true)) return;
  busy.value = 'teardown';
  try { await api('POST', '/api/kernel/teardown'); toast(t('تانل کرنلی حذف شد'), 'ok'); await refresh(); }
  catch (e) { fail(e); }
  busy.value = '';
}

async function loadLogs() {
  try {
    const r = await api('GET', '/api/kernel/logs');
    logs.value = r.logs || '';
    await nextTick();
    if (logsBox.value) logsBox.value.scrollTop = logsBox.value.scrollHeight;
  } catch (e) { fail(e); }
}

async function runPing() {
  pinging.value = true;
  try {
    const r = await api('POST', '/api/kernel/ping');
    const m = {};
    for (const x of r.results || []) m[x.index] = x;
    ping.value = m;
  } catch (e) { fail(e); }
  pinging.value = false;
}

// a ping result as shown in the peers table, in the current language
const pingText = (x) => (x.loss_pct >= 100 ? t('بی‌پاسخ') : t('{rtt} · افت {loss}', { rtt: ms(x.rtt_ms), loss: fa(x.loss_pct) + pct() }));
</script>

<template>
  <div>
    <PageHead :title="$t('تانل کرنلی')">
      <template #title>
        <span v-if="d && !d.installed" class="pill">{{ $t('نصب نیست') }}</span>
        <span v-else-if="d" class="pill" :class="stateClass(main())">{{ $t(STATE_FA[main()] || main() || '—') }}</span>
        <router-link to="/tunnels" class="btn small"><AppIcon name="chev" /> {{ $t('همهٔ تانل‌ها') }}</router-link>
      </template>
      <template v-if="d && d.installed">
        <button v-if="main() === 'active'" class="btn" :disabled="!!busy" @click="act('stop', $t('متوقف شد'))"><AppIcon name="stop" /> {{ $t('توقف') }}</button>
        <button v-else class="btn" :disabled="!!busy" @click="act('start', $t('شروع شد'))"><AppIcon name="play" /> {{ $t('شروع') }}</button>
        <button class="btn" :disabled="!!busy" @click="act('restart', $t('ری‌استارت شد'))"><AppIcon name="redo" :spin="busy === 'restart'" /> {{ $t('ری‌استارت و بازسازی') }}</button>
        <button class="btn danger" :disabled="!!busy" @click="teardown"><AppIcon name="trash" /> {{ $t('حذف تانل کرنلی') }}</button>
      </template>
    </PageHead>

    <div v-if="!d" class="boot"><div class="boot-mark"></div></div>

    <div v-else-if="!d.installed" class="card">
      <div class="card-head"><h3><AppIcon name="tunnel" /> {{ $t('تانل کرنلی نصب نیست') }}</h3>
        <span class="pill" :class="d.base_ready ? 'ok' : 'warn'">{{ $t(d.base_ready ? 'پیش‌نیازها آماده' : 'پیش‌نیاز نصب نشده') }}</span></div>
      <p class="muted">{{ $t('روی این سرور تانل کرنلی (ESP / MOI) برپا نیست. مستقیم از همین‌جا بسازید — پنل این سرور و سرور مقابل را خودکار و هماهنگ تنظیم می‌کند.') }}</p>
      <button class="btn primary" @click="store.newTunnelKind = 'kernel'; store.newTunnel = true"><AppIcon name="plus" /> {{ $t('ساخت تانل کرنلی') }}</button>
    </div>

    <template v-else>
      <div class="grid-2">
        <div class="card">
          <div class="card-head"><h3><AppIcon name="tunnel" /> {{ $t('مشخصات') }}</h3></div>
          <div class="kv">
            <div class="k">{{ $t('نقش') }}</div><div>{{ $t(d.role === 'IRAN' ? 'ایران (هاب)' : 'خارج') }}</div>
            <div class="k">{{ $t('نوع تانل') }}</div><div>{{ $t(MODE_FA[d.mode] || d.mode) }}</div>
            <div class="k">{{ $t('توزیع ترافیک') }}</div><div>{{ d.balance }}</div>
            <div class="k">{{ $t('IP این سرور') }}</div><div class="ltr">{{ d.self_ip }}</div>
            <div class="k">MTU</div><div class="ltr">{{ d.mtu }}</div>
            <div class="k">{{ $t('پروفایل') }}</div><div>{{ d.profile }}</div>
            <template v-if="d.shape_mbit && d.shape_mbit !== '0'"><div class="k">{{ $t('محدودیت سرعت') }}</div><div class="ltr">{{ d.shape_mbit }} Mbit/s</div></template>
            <div class="k">{{ $t('IP واقعی کاربر') }}</div><div>{{ $t(d.keep_client_ip ? 'حفظ می‌شود' : 'خاموش') }}</div>
            <template v-if="d.install_date"><div class="k">{{ $t('تاریخ نصب') }}</div><div class="ltr">{{ d.install_date }}</div></template>
          </div>
        </div>
        <div class="card">
          <div class="card-head"><h3><AppIcon name="gear" /> {{ $t('سرویس‌ها') }}</h3></div>
          <div class="table-wrap">
            <table>
              <thead><tr><th>SERVICE</th><th>STATE</th><th>ENABLED</th><th>RESTARTS</th></tr></thead>
              <tbody>
                <tr v-for="(st, u) in d.units" :key="u">
                  <td>{{ $t(UNIT_FA[u] || u) }}</td>
                  <td><span class="pill" :class="stateClass(st.active)">{{ $t(STATE_FA[st.active] || st.active) }}</span></td>
                  <td>{{ st.enabled || '—' }}</td>
                  <td class="num">{{ fa(st.restarts || 0) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-head"><h3><AppIcon name="net" /> {{ $t('همتاها (سرورهای خارج)') }}</h3>
          <button class="btn small" :disabled="pinging" @click="runPing"><span v-if="pinging" class="spin"></span><AppIcon v-else name="pulse" /> {{ $t('پینگ همتاها') }}</button></div>
        <div v-if="d.peers.length" class="table-wrap">
          <table>
            <thead><tr><th>#</th><th>NAME</th><th>PEER IP</th><th>IFACE</th><th>PORTS</th><th>HEALTH</th><th>PING</th></tr></thead>
            <tbody>
              <tr v-for="p in d.peers" :key="p.index">
                <td class="num">{{ fa(p.index) }}</td>
                <td><i class="dot" :class="(HEALTH[p.health] || HEALTH.unknown)[0]"></i>&nbsp; {{ p.name }}</td>
                <td class="ltr">{{ p.ip }}</td>
                <td class="ltr">{{ p.iface }}{{ p.iface_up ? '' : ' ✕' }}</td>
                <td class="ltr">{{ [p.tcp, p.udp && p.udp !== p.tcp ? 'udp:' + p.udp : ''].filter(Boolean).join(' ') || '—' }}</td>
                <td><span class="pill" :class="(HEALTH[p.health] || HEALTH.unknown)[0]">{{ $t((HEALTH[p.health] || HEALTH.unknown)[1]) }}</span></td>
                <td class="ltr faint">{{ ping[p.index] ? pingText(ping[p.index]) : '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else class="empty">{{ $t('همتایی تعریف نشده') }}</div>
      </div>

      <div class="card">
        <div class="card-head"><h3><AppIcon name="list" /> {{ $t('لاگ‌ها') }}</h3><button class="btn small" @click="loadLogs"><AppIcon name="redo" /> {{ $t('بارگذاری') }}</button></div>
        <pre v-if="logs !== null" ref="logsBox" class="logs">{{ logs || $t('(خالی)') }}</pre>
      </div>
    </template>

  </div>
</template>
