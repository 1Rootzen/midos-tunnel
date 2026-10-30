<script setup>
defineProps({ title: { type: String, required: true }, tag: { type: String, default: '' } });
</script>

<template>
  <div class="page-head">
    <div class="head-l">
      <h2>{{ title }}</h2>
      <slot name="title" />
    </div>
    <div class="actions">
      <span v-if="tag" class="tag">{{ tag }}</span>
      <slot />
    </div>
  </div>
</template>
