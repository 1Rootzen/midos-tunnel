<script setup>
import { computed } from 'vue';
import { applyMaxStealth } from '../lib/constants.js';
import FormField from './FormField.vue';
import AppIcon from './AppIcon.vue';

// What a direct cable does against filtering: the server name its TLS hello
// carries, splitting that hello, renewing carriers, the real site probes meet
// (decoy / camouflage) and padding. It edits the cable it is given in place;
// "change" lets a caller that saves on every edit (the designer) know.
const props = defineProps({
  l: { type: Object, required: true },
  disabled: { type: Boolean, default: false },
});
const emit = defineEmits(['change']);
const tls = computed(() => (props.l.transport || 'tls') === 'tls');
const padOK = computed(() => tls.value || props.l.transport === 'ws');

function maxStealth() {
  applyMaxStealth(props.l);
  emit('change');
}
</script>

<template>
  <div class="stealth-fields">
    <div class="stealth-head">
      <div class="section-title">{{ $t('امنیت در برابر فیلترینگ') }}</div>
      <button type="button" class="btn small" :disabled="disabled" @click="maxStealth"><AppIcon name="shield" /> {{ $t('حداکثر امنیت') }}</button>
    </div>
    <p class="hint">{{ $t('«حداکثر امنیت» ترنسپورت را TLS روی ۴۴۳ می‌کند و همهٔ گزینه‌های زیر را روشن می‌کند؛ سرعت کمی کم می‌شود ولی تشخیص تانل بسیار سخت‌تر است.') }}</p>
    <div class="row">
      <FormField :label="$t('نام سرور در دست‌دهی TLS (SNI)')" :hint="$t('سایتی معروف که در ایران باز است؛ مثل www.microsoft.com')">
        <input v-model.trim="l.server_name" type="text" dir="ltr" placeholder="www.cloudflare.com" :disabled="disabled" @input="emit('change')" />
      </FormField>
      <FormField :label="$t('سایت واقعی برای کاوشگرها')" :hint="$t('با استتار: همان سایت روی ۴۴۳ (مثل www.microsoft.com:443)؛ بدون استتار: نسخهٔ HTTP آن (:80)')">
        <input v-model.trim="l.decoy" type="text" dir="ltr" placeholder="www.microsoft.com:443" :disabled="disabled" @input="emit('change')" />
      </FormField>
    </div>
    <label class="check"><input v-model="l.camouflage" type="checkbox" :disabled="disabled || !tls" @change="emit('change')" />
      {{ $t('استتار: هر کس بدون کلید تانل وصل شود، گواهی و صفحهٔ واقعی همان سایت را می‌بیند (فقط ترنسپورت TLS)') }}</label>
    <label class="check"><input v-model="l.tls_fragment" type="checkbox" :disabled="disabled || !tls" @change="emit('change')" />
      {{ $t('قطعه‌قطعه کردن ClientHello تا نام سرور خوانده نشود') }}</label>
    <label class="check"><input v-model="l.pad" type="checkbox" :disabled="disabled || !padOK" @change="emit('change')" />
      {{ $t('پرکردن تصادفی ترافیک تا اندازهٔ بسته‌ها الگوی کاربر را لو ندهد (TLS و WebSocket)') }}</label>
    <FormField :label="$t('نو کردن اتصال‌ها (ثانیه)')" :hint="$t('۰ = خاموش؛ وگرنه حداقل ۳۰. اتصال‌های طولانی به یک IP خارجی کمتر به چشم می‌آیند.')">
      <input v-model.number="l.rotate_sec" type="number" min="0" dir="ltr" :disabled="disabled" @input="emit('change')" />
    </FormField>
  </div>
</template>

<style>
.stealth-fields { margin-top: 10px; padding-top: 10px; border-top: 1px dashed var(--line-2); }
.stealth-fields .check { display: flex; gap: 8px; align-items: flex-start; margin: 6px 0; }
</style>
