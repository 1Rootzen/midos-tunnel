<script setup>
import { ref, computed, onMounted } from 'vue';
import { api } from '../lib/api.js';
import { fail, toast } from '../lib/toast.js';
import { t } from '../lib/i18n.js';
import PageHead from '../components/PageHead.vue';
import AppIcon from '../components/AppIcon.vue';

// Doctor: a read-only health check with a one-click fix for safe repairs.
const LABEL = { ok: 'سالم', warn: 'هشدار', fail: 'خطا' };
const checks = ref([]);
const loading = ref(true);
const repairing = ref(false);

const bad = computed(() => checks.value.filter((c) => c.status !== 'ok').length);
const fixes = computed(() => [...new Set(checks.value.filter((c) => c.fix).map((c) => c.fix))]);

async function load() {
  loading.value = true;
  try { checks.value = (await api('GET', '/api/doctor')).checks || []; }
  catch (e) { fail(e); }
  loading.value = false;
}
async function applyOne(fix) {
  try { checks.value = (await api('POST', '/api/doctor/repair', { fix })).checks || []; toast(t('انجام شد'), 'ok'); }
  catch (e) { fail(e); }
}
async function repairAll() {
  repairing.value = true;
  for (const f of fixes.value) {
    try { await api('POST', '/api/doctor/repair', { fix: f }); } catch (_) { /* keep going; the reload shows what is left */ }
  }
  toast(t('تعمیر سریع انجام شد'), 'ok');
  repairing.value = false;
  load();
}
onMounted(load);
</script>

<template>
  <div>
    <PageHead :title="$t('سلامت')" :tag="loading ? 'DIAGNOSTICS' : (bad ? $t('{n} مورد نیازمند توجه', { n: bad }) : $t('همه چیز سالم است'))">
      <button class="btn" :disabled="loading" @click="load"><AppIcon name="redo" :spin="loading" /> {{ $t('بررسی دوباره') }}</button>
      <button class="btn primary" :disabled="!fixes.length || repairing" @click="repairAll"><span v-if="repairing" class="spin"></span><AppIcon v-else name="heart" /> {{ $t('تعمیر سریع') }}</button>
    </PageHead>
    <div class="tunnels">
      <div v-for="c in checks" :key="c.id" class="lrow card chk" :class="c.status">
        <div class="tname"><i class="dot" :class="c.status === 'ok' ? 'ok' : (c.status === 'warn' ? 'warn' : 'bad')"></i><span>{{ $t(c.title) }}</span></div>
        <div class="tcell grow"><div class="tag">{{ $t(LABEL[c.status] || c.status) }}</div><div class="v">{{ $t(c.detail) }}</div></div>
        <div class="actions"><button v-if="c.fix" class="btn small" @click="applyOne(c.fix)"><AppIcon name="redo" /> {{ $t('تعمیر') }}</button></div>
      </div>
    </div>
  </div>
</template>
