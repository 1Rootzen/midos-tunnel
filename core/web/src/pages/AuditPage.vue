<script setup>
import { ref, onMounted } from 'vue';
import { api } from '../lib/api.js';
import { fail } from '../lib/toast.js';
import { locale } from '../lib/i18n.js';
import PageHead from '../components/PageHead.vue';
import AppIcon from '../components/AppIcon.vue';

// The audit trail: who did what, from where. Read-only.
const EVENT_FA = {
  'login': ['ورود', 'ok'], 'login-failed': ['ورود ناموفق', 'bad'], 'logout': ['خروج', ''],
  'password': ['تغییر رمز', 'accent'], '2fa': ['ورود دومرحله‌ای', 'accent'], 'recovery-codes': ['کدهای بازیابی', 'accent'],
  'panel-start': ['شروع پنل', ''], 'panel-cert': ['گواهی پنل', 'accent'], 'telegram': ['تلگرام', 'accent'], 'alerts': ['هشدارها', 'accent'],
  'update': ['به‌روزرسانی', 'accent'], 'rollback': ['بازگشت نسخه', 'accent'],
  'tunnel-create': ['ساخت تانل', 'ok'], 'tunnel-delete': ['حذف تانل', 'bad'], 'tunnel-update': ['ویرایش تانل', 'accent'],
  'tunnel-restart': ['ری‌استارت تانل', ''], 'tunnel-start': ['شروع تانل', ''], 'tunnel-stop': ['توقف تانل', ''],
  'kernel-create': ['ساخت تانل کرنلی', 'ok'], 'watchdog': ['واچ‌داگ', 'accent'], 'limit': ['سقف ترافیک', 'accent'],
  'doctor': ['تعمیر', 'accent'], 'topology-link': ['اتصال سمت مقابل', 'ok'], 'bench': ['بنچمارک', ''],
};
const rows = ref([]);
const loading = ref(true);

const when = (unix) => new Date(unix * 1000).toLocaleString(locale(), { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' });
const ev = (e) => EVENT_FA[e] || [e, ''];

async function load() {
  loading.value = true;
  try { rows.value = await api('GET', '/api/audit'); }
  catch (e) { fail(e); }
  loading.value = false;
}
onMounted(load);
</script>

<template>
  <div>
    <PageHead :title="$t('ممیزی')" tag="AUDIT TRAIL">
      <button class="btn small" :disabled="loading" @click="load"><AppIcon name="redo" :spin="loading" /> {{ $t('تازه‌سازی') }}</button>
    </PageHead>
    <div class="card">
      <div class="table-wrap">
        <table>
          <thead><tr><th>{{ $t('زمان') }}</th><th>{{ $t('رویداد') }}</th><th>{{ $t('جزئیات') }}</th><th>{{ $t('آدرس') }}</th></tr></thead>
          <tbody>
            <tr v-for="(e, i) in rows" :key="i">
              <td class="faint num">{{ when(e.t) }}</td>
              <td><span class="pill" :class="ev(e.event)[1]">{{ $t(ev(e.event)[0]) }}</span></td>
              <td>{{ e.detail || '—' }}</td>
              <td class="ltr">{{ e.ip || '—' }}</td>
            </tr>
            <tr v-if="!loading && !rows.length"><td colspan="4" class="empty">{{ $t('رویدادی ثبت نشده') }}</td></tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>
