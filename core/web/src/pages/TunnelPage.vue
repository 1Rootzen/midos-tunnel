<script setup>
import { ref, computed, shallowRef, nextTick } from 'vue';
import { useRouter } from 'vue-router';
import { api } from '../lib/api.js';
import { usePoll } from '../lib/poll.js';
import { fail, toast, copyText } from '../lib/toast.js';
import { confirmBox } from '../lib/confirm.js';
import { fa, bytes, ms, since } from '../lib/format.js';
import { trName, LISTENERS, STATE_FA, stateClass, presetName } from '../lib/constants.js';
import { summary, roleText } from '../lib/summary.js';
import PageHead from '../components/PageHead.vue';
import AppIcon from '../components/AppIcon.vue';
import { t as tx, pct } from '../lib/i18n.js';

const props = defineProps({ id: { type: String, required: true } });
const router = useRouter();
const t = shallowRef(null);
const busy = ref('');
const logs = ref(null);
const logsBox = ref(null);
const test = ref(null);
const testing = ref(false);
const peer = ref(null);
const path = '/api/tunnels/' + encodeURIComponent(props.id);

usePoll(async () => {
  try {
    t.value = await api('GET', path);
  } catch (e) {
    if (!t.value) router.replace('/tunnels');
    throw e;
  }
}, 3000);

const sm = computed(() => (t.value ? summary(t.value) : null));
const running = computed(() => t.value && t.value.state.active === 'active');
const endpoints = computed(() => {
  const c = sm.value.c;
  if (!sm.value.dials) return [];
  return c.endpoints || (c.server ? [{ transport: c.transport || 'tls', server: c.server }] : []);
});
const listeners = computed(() => {
  const c = sm.value.c;
  const out = LISTENERS.filter(([key]) => c[key]).map(([key, tr]) => [trName(tr), c[key]]);
  if (c.http_listen) out.push(['HTTP-WS', c.http_listen]);
  return out;
});
const isActiveEp = (e) => sm.value.s && sm.value.s.endpoint && sm.value.s.endpoint.server === e.server && sm.value.s.endpoint.transport === e.transport;
const portsText = computed(() => {
  const c = sm.value.c;
  return [(c.ports || []).join(', '), (c.udp_ports || []).length ? 'udp: ' + c.udp_ports.join(', ') : ''].filter(Boolean).join('  |  ') || '—';
});

async function act(action, label) {
  busy.value = action;
  try { t.value = await api('POST', path + '/action', { action }); toast(label, 'ok'); }
  catch (e) { fail(e); }
  busy.value = '';
}

async function remove() {
  if (!await confirmBox(tx('حذف تانل'), tx('تانل «{id}» متوقف و کانفیگ آن حذف می‌شود.', { id: props.id }), tx('حذف'), true)) return;
  try { await api('DELETE', path); toast(tx('حذف شد'), 'ok'); router.push('/tunnels'); }
  catch (e) { fail(e); }
}

async function enableStats() {
  try { t.value = await api('POST', path + '/stats'); toast(tx('آمار فعال شد'), 'ok'); }
  catch (e) { fail(e); }
}

async function loadLogs() {
  try {
    const r = await api('GET', path + '/logs?lines=300');
    logs.value = r.logs || tx('(خالی)');
    await nextTick();
    if (logsBox.value) logsBox.value.scrollTop = logsBox.value.scrollHeight;
  } catch (e) { fail(e); }
}

async function runTest() {
  testing.value = true;
  test.value = null;
  try { test.value = await api('POST', path + '/test'); }
  catch (e) { fail(e); }
  testing.value = false;
}

async function showPeer() {
  try { peer.value = await api('POST', path + '/peer'); }
  catch (e) { fail(e); }
}
</script>

<template>
  <div v-if="t">
    <PageHead :title="id">
      <template #title><span class="pill" :class="stateClass(t.state.active)">{{ $t(STATE_FA[t.state.active] || t.state.active) }}</span></template>
      <button v-if="running" class="btn" :disabled="!!busy" @click="act('stop', $t('متوقف شد'))"><AppIcon name="stop" /> {{ $t('توقف') }}</button>
      <button v-else class="btn" :disabled="!!busy" @click="act('start', $t('شروع شد'))"><AppIcon name="play" /> {{ $t('شروع') }}</button>
      <button class="btn" :disabled="!!busy" @click="act('restart', $t('راه‌اندازی مجدد شد'))"><AppIcon name="redo" :spin="busy === 'restart'" /> {{ $t('ری‌استارت') }}</button>
      <router-link class="btn" :to="'/edit/' + encodeURIComponent(id)"><AppIcon name="edit" /> {{ $t('ویرایش') }}</router-link>
      <button class="btn" @click="showPeer"><AppIcon name="link" /> {{ $t('راه‌اندازی سمت مقابل') }}</button>
      <button v-if="t.managed" class="btn danger" @click="remove"><AppIcon name="trash" /> {{ $t('حذف') }}</button>
    </PageHead>

    <div v-if="t.stats_error === 'disabled'" class="card notice">
      <p class="muted">{{ $t('آمار زنده برای این تانل خاموش است (تانل قبل از پنل ساخته شده). روشن کردنش یک ری‌استارت کوتاه لازم دارد.') }}</p>
      <button class="btn primary" @click="enableStats"><AppIcon name="pulse" /> {{ $t('فعال‌سازی آمار زنده') }}</button>
    </div>

    <div class="grid-2">
      <div class="card">
        <div class="card-head"><h3><AppIcon name="tunnel" /> {{ $t('مشخصات') }}</h3></div>
        <div class="kv">
          <div class="k">{{ $t('نقش') }}</div><div>{{ roleText(sm.c) }}</div>
          <div class="k">{{ $t('سرویس') }}</div><div class="ltr">{{ t.unit }}</div>
          <template v-if="sm.dials">
            <div class="k">{{ $t('مسیرها') }}</div>
            <div>
              <div v-for="(e, i) in endpoints" :key="i" class="ep-line">
                <span class="pill" :class="{ accent: isActiveEp(e) }">{{ trName(e.transport) }}</span>
                <span class="ltr">{{ e.server }}</span><span v-if="i" class="faint">{{ $t('(پشتیبان)') }}</span>
              </div>
            </div>
          </template>
          <template v-else>
            <div class="k">{{ $t('شنود') }}</div>
            <div><div v-for="[n, a] in listeners" :key="n" class="ep-line"><span class="pill">{{ n }}</span><span class="ltr">{{ a }}</span></div></div>
          </template>
          <div class="k">{{ $t('پورت‌ها') }}</div><div class="ltr">{{ portsText }}</div>
          <div class="k">{{ $t('IP واقعی کاربر') }}</div><div>{{ sm.c.proxy_protocol ? $t('ارسال می‌شود (PROXY v2)') : $t('خاموش') }}</div>
          <div class="k">{{ $t('پریست سرعت') }}</div><div>{{ presetName(sm.c.preset || 'balance') }}</div>
          <template v-if="sm.c.tls_fragment || sm.c.shape || sm.c.rotate_sec">
            <div class="k">{{ $t('ضد DPI') }}</div>
            <div>
              <span v-if="sm.c.tls_fragment" class="pill accent">{{ $t('ClientHello قطعه‌قطعه') }}</span>
              <span v-if="sm.c.shape" class="pill accent">{{ $t('شکل‌دهی') }}</span>
              <span v-if="sm.c.rotate_sec" class="pill accent">{{ $t('چرخش {n}s', { n: fa(sm.c.rotate_sec) }) }}</span>
            </div>
          </template>
          <template v-if="sm.s"><div class="k">{{ $t('نسخه / فعال از') }}</div><div><span class="ltr">{{ sm.s.version }}</span> · {{ since(Math.floor(Date.now() / 1000) - sm.s.uptime_s) }}</div></template>
          <template v-if="t.state.restarts"><div class="k">{{ $t('ری‌استارت خودکار') }}</div><div>{{ fa(t.state.restarts) }}</div></template>
          <template v-if="sm.s && sm.s.last_error"><div class="k">{{ $t('آخرین خطا') }}</div><div class="ltr">{{ sm.s.last_error }}</div></template>
          <template v-if="t.config_error"><div class="k">{{ $t('خطای کانفیگ') }}</div><div class="ltr">{{ t.config_error }}</div></template>
        </div>
      </div>
      <div class="card">
        <div class="card-head"><h3><AppIcon name="pulse" :class="{ live: running }" /> {{ $t('حامل‌های زنده') }}</h3><span class="tag">CARRIERS</span></div>
        <div v-if="sm.s && sm.s.carriers.length" class="table-wrap">
          <table>
            <thead><tr><th>LINK</th><th>PEER</th><th>RTT</th><th>JITTER</th><th>RETRANS</th><th>UP FOR</th></tr></thead>
            <tbody>
              <tr v-for="(x, i) in sm.s.carriers" :key="i">
                <td><span class="pill accent">{{ trName(x.transport) }}</span></td>
                <td class="ltr">{{ x.peer }}</td>
                <td class="num">{{ x.measured ? ms(x.rtt_ms) : '—' }}</td>
                <td class="num">{{ x.measured ? ms(x.rtt_var_ms) : '—' }}</td>
                <td class="num">{{ x.measured ? fa(x.retrans) : '—' }}</td>
                <td>{{ since(x.up_since) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else class="empty">{{ running ? (sm.s ? $t('هیچ حاملی وصل نیست') : $t('آمار در دسترس نیست')) : $t('تانل متوقف است') }}</div>
      </div>
    </div>

    <div class="card">
      <div class="card-head"><h3><AppIcon name="net" /> {{ $t('ترافیک پورت‌ها') }}</h3></div>
      <div v-if="sm.s && sm.s.ports.length" class="table-wrap">
        <table>
          <thead><tr><th>PROTO</th><th>PORT</th><th>UP</th><th>DOWN</th><th>ACTIVE</th><th>TOTAL</th></tr></thead>
          <tbody>
            <tr v-for="p in sm.s.ports" :key="p.proto + p.port">
              <td>{{ p.proto.toUpperCase() }}</td><td class="ltr">{{ p.port }}</td>
              <td class="num">{{ bytes(p.bytes_up) }}</td><td class="num">{{ bytes(p.bytes_down) }}</td>
              <td class="num">{{ fa(p.streams_active) }}</td><td class="num">{{ fa(p.streams_total) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty">{{ $t('هنوز ترافیکی عبور نکرده') }}</div>
    </div>

    <div class="card">
      <div class="card-head"><h3><AppIcon name="pulse" /> {{ $t('تست اتصال') }}</h3>
        <button class="btn small" :disabled="testing" @click="runTest"><AppIcon name="pulse" /> {{ $t('اجرای تست') }}</button></div>
      <div v-if="testing" class="muted"><span class="spin"></span> {{ $t('در حال آزمایش (حدود ۱۵ ثانیه)…') }}</div>
      <template v-else-if="test">
        <div v-if="(test.results || []).length" class="table-wrap">
          <table>
            <thead><tr><th>TARGET</th><th>KIND</th><th>LOSS</th><th>MIN / AVG / MAX</th><th></th></tr></thead>
            <tbody>
              <tr v-for="(x, i) in test.results" :key="i">
                <td class="ltr">{{ x.target }}</td><td>{{ x.kind.toUpperCase() }}</td>
                <td><span v-if="x.error" class="pill bad">{{ x.error }}</span><span v-else class="num">{{ x.sent ? fa(x.loss_pct) + pct() : '—' }}</span></td>
                <td class="num">{{ x.avg_ms ? ms(x.min_ms) + ' / ' + ms(x.avg_ms) + ' / ' + ms(x.max_ms) : '—' }}</td>
                <td class="faint">{{ x.comment || '' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else class="empty">{{ $t('این سمت تانل فقط گوش می‌دهد؛ برای آزمایش، تست را در سمت مقابل اجرا کنید یا حامل‌های زنده را ببینید.') }}</div>
        <div v-if="test.listening && test.listening.length" class="stat-sub">{{ $t('گوش می‌دهد روی:') }} <span class="ltr">{{ test.listening.join(', ') }}</span></div>
      </template>
    </div>

    <div class="card">
      <div class="card-head"><h3><AppIcon name="link" /> {{ $t('سمت مقابل') }}</h3></div>
      <template v-if="peer">
        <p class="muted">{{ $t('این دستور را روی سرور مقابل (با کاربر root) اجرا کنید. باینری، کانفیگ و سرویس را نصب و فایروال را باز می‌کند. لینک تا ۲ ساعت معتبر است و کلید تانل را در خود دارد؛ آن را جایی منتشر نکنید.') }}</p>
        <div class="cmd"><pre class="code">{{ peer.command }}</pre><button class="btn icon-only" :title="$t('کپی')" @click="copyText(peer.command)"><AppIcon name="copy" /></button></div>
        <details class="adv peer-cfg"><summary>{{ $t('نمایش کانفیگ سمت مقابل') }}</summary><pre class="code">{{ peer.config }}</pre></details>
      </template>
      <div v-else class="faint">{{ $t('دکمهٔ «راه‌اندازی سمت مقابل» دستور نصب را این‌جا نشان می‌دهد.') }}</div>
    </div>

    <div class="card">
      <div class="card-head"><h3><AppIcon name="list" /> {{ $t('لاگ‌ها') }}</h3><button class="btn small" @click="loadLogs"><AppIcon name="redo" /> {{ $t('بارگذاری') }}</button></div>
      <pre v-if="logs !== null" ref="logsBox" class="logs">{{ logs }}</pre>
    </div>
  </div>
  <div v-else class="boot"><div class="boot-mark"></div></div>
</template>

<style>
.notice { margin-bottom: 16px; }
.ep-line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 4px; }
.peer-cfg { margin-top: 14px; }
</style>
