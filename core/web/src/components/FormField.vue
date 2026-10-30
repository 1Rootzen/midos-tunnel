<script setup>
defineProps({ label: { type: String, default: '' }, hint: { type: String, default: '' } });
</script>

<template>
  <div class="field">
    <label v-if="label">{{ label }}</label>
    <slot />
    <div v-if="hint" class="hint">{{ hint }}</div>
  </div>
</template>
