<script setup>
import { computed } from 'vue';
import { benchGet, speedRows } from './model.js';
import { fa } from '../lib/format.js';
import { t, locale } from '../lib/i18n.js';
import AppIcon from '../components/AppIcon.vue';

// The bench.sh result of one server: live output while it runs, then its
// specs, disk I/O and network speeds.
const props = defineProps({ node: { type: Object, required: true }, bench: { type: Object, required: true } });
const emit = defineEmits(['run']);
const run = computed(() => props.bench.running[props.node.id]);
const r = computed(() => props.bench.results[props.node.id]);
const tail = computed(() => (run.value && run.value.tail ? run.value.tail.split('\n').filter((x) => x.trim()).slice(-14).join('\n') : '…'));
const specs = computed(() => {
  if (!r.value) return [];
  const x = r.value;
  const rows = [
    [t('پردازنده'), benchGet(x, 'CPU Model')], [t('هسته‌ها'), benchGet(x, 'CPU Cores')], ['AES-NI', benchGet(x, 'AES-NI')],
    [t('حافظه'), benchGet(x, 'Total Mem', 'Total RAM')], [t('دیسک'), benchGet(x, 'Total Disk')], [t('مجازی‌سازی'), benchGet(x, 'Virtualization')],
    [t('کرنل'), benchGet(x, 'Kernel')], [t('سیستم‌عامل'), benchGet(x, 'OS')], [t('کنترل ازدحام'), benchGet(x, 'TCP CC', 'TCP Congestion Ctrl')],
    [t('دیتاسنتر'), benchGet(x, 'Organization')], [t('موقعیت'), benchGet(x, 'Location')],
  ];
  for (const io of x.io || []) rows.push([/average/i.test(io.key) ? t('I/O میانگین') : io.key.replace('I/O Speed', 'I/O'), io.value]);
  return rows.filter(([, v]) => v);
});
const speeds = computed(() => speedRows(r.value));
const gbpsFmt = {};
const gbps = () => (gbpsFmt[locale()] ||= new Intl.NumberFormat(locale(), { maximumFractionDigits: 2 }));
const rate = (mbps) => (mbps >= 1000 ? gbps().format(mbps / 1000) + ' Gbps' : fa(Math.round(mbps)) + ' Mbps');
const pingClass = (p) => (p < 80 ? 'good' : p < 200 ? 'mid' : 'slow');
</script>

<template>
  <div class="tp-bench">
    <div class="tp-bench-head">
      <b>{{ $t('بنچمارک سرور (bench.sh)') }}</b>
      <button class="btn small" :disabled="!!run" @click="emit('run')"><span v-if="run" class="spin"></span><AppIcon v-else name="gauge" /> {{ run ? $t('در حال اجرا…') : (r ? $t('اجرای دوباره') : $t('اجرای بنچمارک')) }}</button>
    </div>
    <template v-if="run">
      <div class="muted">{{ $t('bench.sh در حال اجراست؛ معمولاً ۳ تا ۸ دقیقه طول می‌کشد (تست CPU، دیسک و سرعت شبکه).') }}</div>
      <pre class="code tp-bench-tail">{{ tail }}</pre>
    </template>
    <template v-else-if="r">
      <div v-if="!r.ok" class="hint warn">{{ $t('بنچمارک ناموفق بود: {e}', { e: r.error || '' }) }}</div>
      <dl v-if="specs.length" class="tp-bench-grid">
        <template v-for="[k, v] in specs" :key="k"><dt>{{ k }}</dt><dd>{{ v }}</dd></template>
      </dl>
      <div v-if="speeds.length" class="tp-bench-speed">
        <div class="stat-sub">{{ $t('سرعت شبکه تا هر کشور') }}</div>
        <div v-if="speeds.every((s) => s.failed)" class="hint warn">{{ $t('تست سرعت روی هیچ سروری انجام نشد. اگر این نتیجه مربوط به نسخه‌های پیش از ۲.۴.۳ است، بنچمارک را دوباره اجرا کنید.') }}</div>
        <table class="tp-sp">
          <thead><tr><th>{{ $t('کشور') }}</th><th>{{ $t('پینگ') }}</th><th>{{ $t('دانلود') }}</th><th>{{ $t('آپلود') }}</th></tr></thead>
          <tbody>
            <tr v-for="s in speeds" :key="s.key" :class="{ failed: s.failed }">
              <td>
                <div class="tp-sp-country"><span v-if="s.cc" class="tp-sp-cc">{{ s.cc }}</span>{{ s.country }}</div>
                <div class="tp-sp-city">{{ s.city }}</div>
              </td>
              <template v-if="s.failed"><td colspan="3" class="tp-sp-fail">{{ $t('تست ناموفق') }}</td></template>
              <template v-else>
                <td class="num" :class="'p-' + pingClass(s.ping)">{{ s.ping == null ? '—' : fa(s.ping < 10 ? s.ping.toFixed(1) : Math.round(s.ping)) + ' ms' }}</td>
                <td class="num">{{ rate(s.down) }}<i class="tp-sp-bar" :style="{ width: Math.max(4, s.share * 100) + '%' }"></i></td>
                <td class="num">{{ rate(s.up) }}</td>
              </template>
            </tr>
          </tbody>
        </table>
      </div>
      <details class="adv"><summary>{{ $t('خروجی کامل bench.sh') }}</summary><pre class="code tp-bench-tail tp-bench-raw">{{ r.raw || '' }}</pre></details>
      <div class="stat-sub">{{ $t('آخرین اجرا: {at}', { at: new Date(r.at * 1000).toLocaleString(locale()) }) }}{{ r.host && r.host !== 'local' ? ' · ' + r.host : '' }}</div>
    </template>
    <div v-else class="muted">
      {{ node.kind === 'self' ? $t('مشخصات و سرعت همین سرور را می‌سنجد.') : $t('بعد از افزودن سرور خودکار اجرا می‌شود؛ یا همین حالا دستی اجرا کنید.') }}
    </div>
  </div>
</template>
