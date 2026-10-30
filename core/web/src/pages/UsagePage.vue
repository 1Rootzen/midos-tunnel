<script setup>
import { ref, computed, shallowRef } from 'vue';
import { api } from '../lib/api.js';
import { usePoll } from '../lib/poll.js';
import { fail, toast } from '../lib/toast.js';
import { confirmBox } from '../lib/confirm.js';
import { bytes } from '../lib/format.js';
import { t } from '../lib/i18n.js';
import PageHead from '../components/PageHead.vue';
import GlassWindow from '../components/GlassWindow.vue';
import FormField from '../components/FormField.vue';
import AppIcon from '../components/AppIcon.vue';

// Traffic: how much each tunnel moved this month and in total, with an
// optional monthly limit that stops the tunnel when reached.
const GiB = 1024 * 1024 * 1024;
const rows = shallowRef([]);
const loaded = ref(false);
const editing = ref(null);
const gb = ref('');

const { refresh } = usePoll(async () => { rows.value = await api('GET', '/api/usage'); loaded.value = true; }, 5000);

const totals = computed(() => rows.value.reduce((a, r) => ({ month: a.month + (r.month_bytes || 0), total: a.total + (r.total_bytes || 0) }), { month: 0, total: 0 }));
const pct = (r) => (r.limit_bytes ? Math.min(100, 100 * (r.month_bytes || 0) / r.limit_bytes) : 0);
const barClass = (r) => (r.limit_hit ? 'bad' : (pct(r) > 85 ? 'warn' : ''));

function editLimit(r) {
  editing.value = r;
  gb.value = r.limit_bytes ? (r.limit_bytes / GiB).toFixed(1) : '';
}
async function saveLimit() {
  const r = editing.value;
  const v = Math.max(0, Math.round((parseFloat(gb.value) || 0) * GiB));
  try { await api('POST', '/api/usage/limit', { id: r.id, limit_bytes: v }); toast(t('ذخیره شد'), 'ok'); editing.value = null; refresh(); }
  catch (e) { fail(e); }
}
async function reset(r) {
  if (!await confirmBox(t('صفر کردن'), t('شمارندهٔ ترافیک «{id}» صفر شود؟', { id: r.id }), t('صفر کن'), true)) return;
  try { await api('POST', '/api/usage/reset', { id: r.id }); toast(t('صفر شد'), 'ok'); refresh(); }
  catch (e) { fail(e); }
}
</script>

<template>
  <div>
    <PageHead :title="$t('ترافیک')" :tag="$t('این ماه {m} · مجموع {total}', { m: bytes(totals.month), total: bytes(totals.total) })" />
    <div class="tunnels">
      <div v-for="r in rows" :key="r.id" class="lrow card">
        <div class="tname"><i class="dot" :class="{ ok: r.active }"></i><span>{{ r.id }}</span>
          <span v-if="r.limit_hit" class="pill bad">{{ $t('به سقف رسید') }}</span><span v-if="!r.managed" class="pill">{{ $t('قدیمی') }}</span></div>
        <div class="tcell"><div class="tag">{{ $t('این ماه') }}</div><div class="v num">{{ bytes(r.month_bytes) }}</div></div>
        <div class="tcell"><div class="tag">{{ $t('مجموع') }}</div><div class="v num">{{ bytes(r.total_bytes) }}</div></div>
        <div class="tcell grow"><div class="tag">{{ r.limit_bytes ? $t('سقف {v}', { v: bytes(r.limit_bytes) }) : $t('سقف') }}</div>
          <div class="v"><div v-if="r.limit_bytes" class="ubar"><div class="ufill" :class="barClass(r)" :style="{ width: Math.max(2, pct(r)) + '%' }"></div></div>
            <span v-else class="faint">{{ $t('بدون سقف') }}</span></div></div>
        <div class="actions">
          <button class="btn small icon-only" :title="$t('سقف ماهانه')" @click="editLimit(r)"><AppIcon name="edit" /></button>
          <button class="btn small icon-only" :title="$t('صفر کردن شمارنده')" @click="reset(r)"><AppIcon name="redo" /></button>
        </div>
      </div>
      <div v-if="loaded && !rows.length" class="card empty">{{ $t('هنوز تانلی نیست.') }}</div>
    </div>

    <GlassWindow v-if="editing" :title="$t('سقف ماهانهٔ {id}', { id: editing.id })" icon="chart" :width="440" @close="editing = null">
      <FormField :label="$t('سقف ماهانه (گیگابایت)')" :hint="$t('خالی یا صفر یعنی بدون محدودیت')">
        <input v-model="gb" type="number" min="0" step="0.1" dir="ltr" @keydown.enter="saveLimit" />
      </FormField>
      <p class="muted">{{ $t('با رسیدن به سقف، تانل متوقف و یک هشدار ارسال می‌شود. ماه بعد خودکار صفر می‌شود.') }}</p>
      <template #foot>
        <button class="btn primary" @click="saveLimit">{{ $t('ذخیره') }}</button>
        <button class="btn" @click="editing = null">{{ $t('انصراف') }}</button>
      </template>
    </GlassWindow>
  </div>
</template>
